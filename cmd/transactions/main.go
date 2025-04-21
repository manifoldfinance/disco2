package main

import (
	"context" // Import context
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http" // Import http
	"os"       // Import the os package
	"strconv"  // Import strconv
	"strings"  // Import strings
	"time"     // Import time

	"github.com/go-redis/redis/v8" // Import redis
	"github.com/google/uuid"       // Import uuid
	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"  // Import codes
	"google.golang.org/grpc/status" // Import status

	"github.com/knadh/koanf/parsers/dotenv"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // PostgreSQL driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // File source

	// Import generated protobuf code
	transactionspb "github.com/manifoldfinance/disco2/v2/transactions/transactions"
)

// Config holds the application configuration
type Config struct {
	DBDSN     string `koanf:"db_dsn"`
	RedisAddr string `koanf:"redis_addr"`
	HTTPPort  string `koanf:"http_port"`
	GRPCPort  string `koanf:"grpc_port"`
}

var k = koanf.New(".")

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() (*Config, error) {
	// Set default values
	k.Set("db_dsn", "user=user dbname=transactions sslmode=disable")
	k.Set("redis_addr", "localhost:6379")
	k.Set("http_port", ":8081")
	k.Set("grpc_port", ":50052")

	// Load environment variables prefixed with TRANSACTIONS_
	// e.g. TRANSACTIONS_DB_DSN, TRANSACTIONS_REDIS_ADDR
	err := k.Load(env.Provider("TRANSACTIONS_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, "TRANSACTIONS_")), "_", ".", -1)
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("error loading config from env: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("error unmarshalling config: %w", err)
	}

	return &cfg, nil
}

type Server struct {
	transactionspb.UnimplementedTransactionsServer
	db          *sql.DB
	redisClient *redis.Client
}

func main() {
	// Load configuration
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	// Database connection setup
	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	// Redis client setup
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
		DB:   0, // use default DB
	})

	// Ping Redis to check connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")

	// Run database migrations
	m, err := migrate.New(
		"file://transactions/migrations", // Path to migration files
		cfg.DBDSN)                        // Database connection string
	if err != nil {
		log.Fatalf("failed to create migrate instance: %v", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("failed to run migrations: %v", err)
	}
	log.Println("Database migrations applied successfully")

	s := &server{db: db, redisClient: rdb}

	// Set up Echo HTTP server
	e := echo.New()
	// Add HTTP routes here
	e.GET("/transactions", s.listTransactionsHandler)

	// Set up gRPC server (placeholder)
	grpcServer := grpc.NewServer()
	transactionspb.RegisterTransactionsServer(grpcServer, s)

	// Start HTTP server
	go func() {
		log.Printf("HTTP server starting on %s", cfg.HTTPPort)
		if err := e.Start(cfg.HTTPPort); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start http server: %v", err)
		}
	}()

	// Start gRPC server
	lis, err := net.Listen("tcp", cfg.GRPCPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	log.Printf("gRPC server listening on %s", cfg.GRPCPort)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Implement gRPC methods here

func (s *server) RecordTransaction(ctx context.Context, req *transactionspb.TransactionInput) (*transactionspb.Transaction, error) {
	log.Printf("Received RecordTransaction request: %+v", req)

	transactionID := uuid.New().String()
	now := time.Now()
	timestampStr := now.Format(time.RFC3339) // Format timestamp

	query := `INSERT INTO transactions (id, account_id, card_id, amount, currency, merchant_id, merchant_raw, status, created_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			  RETURNING id, account_id, card_id, amount, currency, merchant_id, merchant_raw, status, created_at`

	var createdTxn transactionspb.Transaction
	var cardID sql.NullString
	var merchantID sql.NullString
	var merchantRaw sql.NullString
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, query,
		transactionID,
		req.GetAccountId(),
		sql.NullString{String: req.GetCardId(), Valid: req.GetCardId() != ""},
		req.GetAmount(),
		req.GetCurrency(),
		sql.NullString{String: req.GetMerchantId(), Valid: req.GetMerchantId() != ""},
		sql.NullString{String: req.GetMerchantRaw(), Valid: req.GetMerchantRaw() != ""},
		req.GetStatus(),
		now,
	).Scan(
		&createdTxn.Id,
		&createdTxn.AccountId,
		&cardID,
		&createdTxn.Amount,
		&createdTxn.Currency,
		&merchantID,
		&merchantRaw,
		&createdTxn.Status,
		&createdAt,
	)
	if err != nil {
		log.Printf("failed to insert transaction: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to record transaction")
	}

	createdTxn.CardId = cardID.String
	createdTxn.MerchantId = merchantID.String
	createdTxn.MerchantRaw = merchantRaw.String
	createdTxn.Timestamp = createdAt.Format(time.RFC3339) // Use the DB timestamp

	// Publish "transaction:created" event to Redis
	// Event payload could be JSON or protobuf binary
	eventPayload := fmt.Sprintf(`{"id": "%s", "account_id": "%s", "amount": %d, "currency": "%s", "status": "%s", "timestamp": "%s"}`,
		createdTxn.GetId(),
		createdTxn.GetAccountId(),
		createdTxn.GetAmount(),
		createdTxn.GetCurrency(),
		createdTxn.GetStatus(),
		createdTxn.GetTimestamp(),
	)
	if _, err := s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: "transaction:created",
		MaxLen: 0, // No limit
		Values: map[string]interface{}{
			"payload": eventPayload,
		},
	}).Result(); err != nil {
		log.Printf("failed to publish transaction:created event: %v", err)
		// Depending on requirements, failure to publish event might be critical or just logged
		// For now, just log the error
	} else {
		log.Printf("Published transaction:created event for transaction %s", createdTxn.GetId())
	}

	return &createdTxn, nil
}

func (s *server) GetTransaction(ctx context.Context, req *transactionspb.TransactionQuery) (*transactionspb.Transaction, error) {
	log.Printf("Received GetTransaction request: %+v", req)

	query := `SELECT id, account_id, card_id, amount, currency, merchant_id, merchant_raw, category, status, created_at
			  FROM transactions WHERE id = $1`

	var transaction transactionspb.Transaction
	var cardID sql.NullString
	var merchantID sql.NullString
	var merchantRaw sql.NullString
	var category sql.NullString
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, query, req.GetId()).Scan(
		&transaction.Id,
		&transaction.AccountId,
		&cardID,
		&transaction.Amount,
		&transaction.Currency,
		&merchantID,
		&merchantRaw,
		&category,
		&transaction.Status,
		&createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("transaction not found: %s", req.GetId())
			return nil, status.Errorf(codes.NotFound, "transaction not found")
		}
		log.Printf("failed to get transaction: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get transaction")
	}

	transaction.CardId = cardID.String
	transaction.MerchantId = merchantID.String
	transaction.MerchantRaw = merchantRaw.String
	transaction.Category = category.String
	transaction.Timestamp = createdAt.Format(time.RFC3339)

	return &transaction, nil
}

func (s *server) ListTransactions(ctx context.Context, req *transactionspb.TransactionsQuery) (*transactionspb.TransactionsList, error) {
	log.Printf("Received ListTransactions request: %+v", req)

	query := `SELECT id, account_id, card_id, amount, currency, merchant_id, merchant_raw, category, status, created_at
			  FROM transactions WHERE account_id = $1`
	args := []interface{}{req.GetAccountId()}

	// Add pagination
	if req.GetBeforeId() != "" {
		// Get the timestamp of the before_id transaction
		var beforeTimestamp time.Time
		err := s.db.QueryRowContext(ctx, "SELECT created_at FROM transactions WHERE id = $1", req.GetBeforeId()).Scan(&beforeTimestamp)
		if err != nil {
			if err == sql.ErrNoRows {
				// If before_id not found, just list from the beginning
				log.Printf("before_id transaction not found: %s, listing from beginning", req.GetBeforeId())
			} else {
				log.Printf("failed to get timestamp for before_id: %v", err)
				return nil, status.Errorf(codes.Internal, "failed to get transactions")
			}
		} else {
			query += ` AND created_at < $2`
			args = append(args, beforeTimestamp)
		}
	}

	query += ` ORDER BY created_at DESC`

	if req.GetLimit() > 0 {
		query += fmt.Sprintf(` LIMIT %d`, req.GetLimit())
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("failed to list transactions: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list transactions")
	}
	defer rows.Close()

	var transactions []*transactionspb.Transaction
	for rows.Next() {
		var transaction transactionspb.Transaction
		var cardID sql.NullString
		var merchantID sql.NullString
		var merchantRaw sql.NullString
		var category sql.NullString
		var createdAt time.Time

		if err := rows.Scan(
			&transaction.Id,
			&transaction.AccountId,
			&cardID,
			&transaction.Amount,
			&transaction.Currency,
			&merchantID,
			&merchantRaw,
			&category,
			&transaction.Status,
			&createdAt,
		); err != nil {
			log.Printf("failed to scan transaction row: %v", err)
			return nil, status.Errorf(codes.Internal, "failed to list transactions")
		}

		transaction.CardId = cardID.String
		transaction.MerchantId = merchantID.String
		transaction.MerchantRaw = merchantRaw.String
		transaction.Category = category.String
		transaction.Timestamp = createdAt.Format(time.RFC3339)

		transactions = append(transactions, &transaction)
	}

	if err := rows.Err(); err != nil {
		log.Printf("rows error during listing transactions: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list transactions")
	}

	return &transactionspb.TransactionsList{Items: transactions}, nil
}

func (s *server) UpdateTransaction(ctx context.Context, req *transactionspb.UpdateTransactionRequest) (*transactionspb.Transaction, error) {
	log.Printf("Received UpdateTransaction request: %+v", req)

	// Build the update query dynamically based on provided fields
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.GetMerchantId() != "" {
		updates = append(updates, fmt.Sprintf("merchant_id = $%d", argIndex))
		args = append(args, req.GetMerchantId())
		argIndex++
	}
	if req.GetMerchantName() != "" {
		updates = append(updates, fmt.Sprintf("merchant_name = $%d", argIndex))
		args = append(args, req.GetMerchantName())
		argIndex++
	}
	if req.GetCategory() != "" {
		updates = append(updates, fmt.Sprintf("category = $%d", argIndex))
		args = append(args, req.GetCategory())
		argIndex++
	}
	if req.GetStatus() != "" {
		updates = append(updates, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, req.GetStatus())
		argIndex++
	}

	if len(updates) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "no fields to update")
	}

	query := fmt.Sprintf(`UPDATE transactions SET %s WHERE id = $%d RETURNING id, account_id, card_id, amount, currency, merchant_id, merchant_raw, category, status, created_at`,
		strings.Join(updates, ", "), argIndex)
	args = append(args, req.GetId())

	var updatedTxn transactionspb.Transaction
	var cardID sql.NullString
	var merchantID sql.NullString
	var merchantRaw sql.NullString
	var category sql.NullString
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&updatedTxn.Id,
		&updatedTxn.AccountId,
		&cardID,
		&updatedTxn.Amount,
		&updatedTxn.Currency,
		&merchantID,
		&merchantRaw,
		&category,
		&updatedTxn.Status,
		&createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("transaction not found for update: %s", req.GetId())
			return nil, status.Errorf(codes.NotFound, "transaction not found")
		}
		log.Printf("failed to update transaction: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to update transaction")
	}

	updatedTxn.CardId = cardID.String
	updatedTxn.MerchantId = merchantID.String
	updatedTxn.MerchantRaw = merchantRaw.String
	updatedTxn.Category = category.String
	updatedTxn.Timestamp = createdAt.Format(time.RFC3339)

	return &updatedTxn, nil
}

// Implement HTTP handlers here

func (s *server) listTransactionsHandler(c echo.Context) error {
	accountID := c.QueryParam("account_id")
	if accountID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "account_id query parameter is required"})
	}

	limitStr := c.QueryParam("limit")
	limit := uint32(0)
	if limitStr != "" {
		parsedLimit, err := strconv.ParseUint(limitStr, 10, 32)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid limit parameter"})
		}
		limit = uint32(parsedLimit)
	}

	beforeID := c.QueryParam("before_id")

	req := &transactionspb.TransactionsQuery{
		AccountId: accountID,
		Limit:     limit,
		BeforeId:  beforeID,
	}

	transactionsList, err := s.ListTransactions(c.Request().Context(), req)
	if err != nil {
		// Handle gRPC errors
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
			case codes.InvalidArgument:
				return c.JSON(http.StatusBadRequest, map[string]string{"error": st.Message()})
			case codes.Internal:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			default:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "unknown gRPC error"})
			}
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	return c.JSON(http.StatusOK, transactionsList)
}
