// Package service contains the business logic for the balance service
package service

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/go-redis/redis/v8"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/manifoldfinance/disco2/v2/pkg/pb/balance"
)

// BalanceService implements the Balance service functionality
type BalanceService struct {
	pb.UnimplementedBalanceServer
	db          *sql.DB
	redisClient *redis.Client
}

// NewBalanceService creates a new balance service instance
func NewBalanceService(db *sql.DB, redisClient *redis.Client) *BalanceService {
	return &BalanceService{
		db:          db,
		redisClient: redisClient,
	}
}

// GetBalance retrieves the balance for an account
func (s *BalanceService) GetBalance(ctx context.Context, req *pb.AccountID) (*pb.BalanceResponse, error) {
	log.Printf("Received GetBalance request: %+v", req)

	query := `SELECT balance FROM accounts WHERE account_id = $1`

	var balance int64
	err := s.db.QueryRowContext(ctx, query, req.GetAccountId()).Scan(&balance)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("account not found: %s", req.GetAccountId())
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		log.Printf("failed to get balance: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get balance")
	}

	return &pb.BalanceResponse{
		AccountId:      req.GetAccountId(),
		CurrentBalance: balance,
	}, nil
}

// AuthorizeDebit authorizes and performs a debit operation on an account
func (s *BalanceService) AuthorizeDebit(ctx context.Context, req *pb.AuthorizeDebitRequest) (*pb.DebitResult, error) {
	log.Printf("Received AuthorizeDebit request: %+v", req)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("failed to begin transaction: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to authorize debit")
	}
	defer tx.Rollback() // Rollback if not committed

	// Select balance with FOR UPDATE to lock the row
	query := `SELECT balance FROM accounts WHERE account_id = $1 FOR UPDATE`
	var currentBalance int64
	err = tx.QueryRowContext(ctx, query, req.GetAccountId()).Scan(&currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("account not found for debit: %s", req.GetAccountId())
			return &pb.DebitResult{Success: false, ErrorMessage: "account not found"}, nil
		}
		log.Printf("failed to get balance with lock: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to authorize debit")
	}

	// Check if sufficient funds
	if currentBalance < req.GetAmount() {
		log.Printf("insufficient funds for account %s: current=%d, requested=%d", req.GetAccountId(), currentBalance, req.GetAmount())
		return &pb.DebitResult{Success: false, ErrorMessage: "insufficient funds"}, nil
	}

	// Debit the account
	newBalance := currentBalance - req.GetAmount()
	updateQuery := `UPDATE accounts SET balance = $1, updated_at = NOW() WHERE account_id = $2`
	_, err = tx.ExecContext(ctx, updateQuery, newBalance, req.GetAccountId())
	if err != nil {
		log.Printf("failed to update balance: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to authorize debit")
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		log.Printf("failed to commit transaction: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to authorize debit")
	}

	log.Printf("Successfully debited account %s. New balance: %d", req.GetAccountId(), newBalance)

	// Publish "balance.updated" event to Redis
	if err := s.publishBalanceUpdateEvent(ctx, req.GetAccountId(), newBalance); err != nil {
		log.Printf("failed to publish balance:updated event after debit: %v", err)
		// Log the error but continue - don't fail the operation due to event publishing
	} else {
		log.Printf("Published balance:updated event for account %s", req.GetAccountId())
	}

	return &pb.DebitResult{Success: true, NewBalance: newBalance}, nil
}

// CreditAccount adds credit to an account
func (s *BalanceService) CreditAccount(ctx context.Context, req *pb.CreditRequest) (*pb.BalanceResponse, error) {
	log.Printf("Received CreditAccount request: %+v", req)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("failed to begin transaction: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to credit account")
	}
	defer tx.Rollback() // Rollback if not committed

	// Select balance with FOR UPDATE to lock the row
	query := `SELECT balance FROM accounts WHERE account_id = $1 FOR UPDATE`
	var currentBalance int64
	err = tx.QueryRowContext(ctx, query, req.GetAccountId()).Scan(&currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			// If account not found, create it with the credit amount
			insertQuery := `INSERT INTO accounts (account_id, balance, updated_at) VALUES ($1, $2, NOW())`
			_, insertErr := tx.ExecContext(ctx, insertQuery, req.GetAccountId(), req.GetAmount())
			if insertErr != nil {
				log.Printf("failed to create account and credit: %v", insertErr)
				return nil, status.Errorf(codes.Internal, "failed to credit account")
			}
			newBalance := req.GetAmount()
			if err := tx.Commit(); err != nil {
				log.Printf("failed to commit transaction after create and credit: %v", err)
				return nil, status.Errorf(codes.Internal, "failed to credit account")
			}
			log.Printf("Successfully created and credited account %s. New balance: %d", req.GetAccountId(), newBalance)

			// Publish event after successful account creation and credit
			if err := s.publishBalanceUpdateEvent(ctx, req.GetAccountId(), newBalance); err != nil {
				log.Printf("failed to publish balance:updated event after new account credit: %v", err)
				// Log the error but continue - don't fail the operation due to event publishing
			}

			return &pb.BalanceResponse{AccountId: req.GetAccountId(), CurrentBalance: newBalance}, nil
		}
		log.Printf("failed to get balance with lock: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to credit account")
	}

	// Credit the account
	newBalance := currentBalance + req.GetAmount()
	updateQuery := `UPDATE accounts SET balance = $1, updated_at = NOW() WHERE account_id = $2`
	_, err = tx.ExecContext(ctx, updateQuery, newBalance, req.GetAccountId())
	if err != nil {
		log.Printf("failed to update balance: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to credit account")
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		log.Printf("failed to commit transaction: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to credit account")
	}

	log.Printf("Successfully credited account %s. New balance: %d", req.GetAccountId(), newBalance)

	// Publish "balance.updated" event to Redis
	if err := s.publishBalanceUpdateEvent(ctx, req.GetAccountId(), newBalance); err != nil {
		log.Printf("failed to publish balance:updated event after credit: %v", err)
		// Log the error but continue - don't fail the operation due to event publishing
	} else {
		log.Printf("Published balance:updated event for account %s", req.GetAccountId())
	}

	return &pb.BalanceResponse{AccountId: req.GetAccountId(), CurrentBalance: newBalance}, nil
}

// publishBalanceUpdateEvent publishes a balance update event to Redis
func (s *BalanceService) publishBalanceUpdateEvent(ctx context.Context, accountID string, newBalance int64) error {
	eventPayload := fmt.Sprintf(`{"account_id": "%s", "new_balance": %d}`, accountID, newBalance)
	_, err := s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: "balance:updated",
		MaxLen: 0, // No limit
		Values: map[string]interface{}{
			"payload": eventPayload,
		},
	}).Result()
	return err
}
