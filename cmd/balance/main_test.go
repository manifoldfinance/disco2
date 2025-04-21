package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	balancepb "github.com/manifoldfinance/disco2/v2/balance/balance"
)

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*Server, sqlmock.Sqlmock, redismock.ClientMock) {
	db, mockDb, err := sqlmock.New()
	assert.NoError(t, err)

	mockRedis, mockRedisClient := redismock.NewClientMock()

	s := &Server{
		db:          db,
		redisClient: mockRedisClient,
	}
	return s, mockDb, mockRedisClient
}

func TestGetBalance_Found(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &balancepb.AccountID{AccountId: "acc-123"}
	expectedBalance := int64(10000) // 100.00

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM accounts WHERE account_id = $1`)).
		WithArgs(req.AccountId).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(expectedBalance))

	ctx := context.Background()
	resp, err := s.GetBalance(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, req.AccountId, resp.AccountId)
	assert.Equal(t, expectedBalance, resp.CurrentBalance)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestGetBalance_NotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &balancepb.AccountID{AccountId: "acc-unknown"}

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM accounts WHERE account_id = $1`)).
		WithArgs(req.AccountId).
		WillReturnError(sql.ErrNoRows)

	ctx := context.Background()
	resp, err := s.GetBalance(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestAuthorizeDebit_Success(t *testing.T) {
	s, mockDb, mockRedis := newTestServer(t)
	defer s.db.Close()

	req := &balancepb.AuthorizeDebitRequest{AccountId: "acc-123", Amount: 5000} // Debit 50.00
	currentBalance := int64(10000)                                              // 100.00
	newBalance := currentBalance - req.Amount

	mockDb.ExpectBegin()
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM accounts WHERE account_id = $1 FOR UPDATE`)).
		WithArgs(req.AccountId).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(currentBalance))
	mockDb.ExpectExec(regexp.QuoteMeta(`UPDATE accounts SET balance = $1, updated_at = NOW() WHERE account_id = $2`)).
		WithArgs(newBalance, req.AccountId).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.ExpectCommit()

	mockRedis.ExpectXAdd(&redis.XAddArgs{
		Stream: "balance:updated",
		Values: map[string]interface{}{"payload": redismock.AnyValue},
	}).SetVal("some-stream-id")

	ctx := context.Background()
	resp, err := s.AuthorizeDebit(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, newBalance, resp.NewBalance)
	assert.Empty(t, resp.ErrorMessage)

	assert.NoError(t, mockDb.ExpectationsWereMet())
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestAuthorizeDebit_InsufficientFunds(t *testing.T) {
	s, mockDb, _ := newTestServer(t) // No Redis mock needed if debit fails
	defer s.db.Close()

	req := &balancepb.AuthorizeDebitRequest{AccountId: "acc-123", Amount: 15000} // Debit 150.00
	currentBalance := int64(10000)                                               // 100.00

	mockDb.ExpectBegin()
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM accounts WHERE account_id = $1 FOR UPDATE`)).
		WithArgs(req.AccountId).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(currentBalance))
	mockDb.ExpectRollback() // Expect rollback because funds are insufficient

	ctx := context.Background()
	resp, err := s.AuthorizeDebit(ctx, req)

	assert.NoError(t, err) // Business error, not gRPC error
	assert.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, "insufficient funds", resp.ErrorMessage)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestAuthorizeDebit_AccountNotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &balancepb.AuthorizeDebitRequest{AccountId: "acc-unknown", Amount: 5000}

	mockDb.ExpectBegin()
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM accounts WHERE account_id = $1 FOR UPDATE`)).
		WithArgs(req.AccountId).
		WillReturnError(sql.ErrNoRows)
	mockDb.ExpectRollback()

	ctx := context.Background()
	resp, err := s.AuthorizeDebit(ctx, req)

	assert.NoError(t, err) // Business error
	assert.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, "account not found", resp.ErrorMessage)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestCreditAccount_ExistingAccount(t *testing.T) {
	s, mockDb, mockRedis := newTestServer(t)
	defer s.db.Close()

	req := &balancepb.CreditRequest{AccountId: "acc-123", Amount: 2000} // Credit 20.00
	currentBalance := int64(10000)                                      // 100.00
	newBalance := currentBalance + req.Amount

	mockDb.ExpectBegin()
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM accounts WHERE account_id = $1 FOR UPDATE`)).
		WithArgs(req.AccountId).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(currentBalance))
	mockDb.ExpectExec(regexp.QuoteMeta(`UPDATE accounts SET balance = $1, updated_at = NOW() WHERE account_id = $2`)).
		WithArgs(newBalance, req.AccountId).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.ExpectCommit()

	mockRedis.ExpectXAdd(&redis.XAddArgs{
		Stream: "balance:updated",
		Values: map[string]interface{}{"payload": redismock.AnyValue},
	}).SetVal("some-stream-id")

	ctx := context.Background()
	resp, err := s.CreditAccount(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, req.AccountId, resp.AccountId)
	assert.Equal(t, newBalance, resp.CurrentBalance)

	assert.NoError(t, mockDb.ExpectationsWereMet())
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestCreditAccount_NewAccount(t *testing.T) {
	s, mockDb, mockRedis := newTestServer(t)
	defer s.db.Close()

	req := &balancepb.CreditRequest{AccountId: "acc-new", Amount: 5000} // Credit 50.00
	newBalance := req.Amount

	mockDb.ExpectBegin()
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM accounts WHERE account_id = $1 FOR UPDATE`)).
		WithArgs(req.AccountId).
		WillReturnError(sql.ErrNoRows) // Simulate account not found
	mockDb.ExpectExec(regexp.QuoteMeta(`INSERT INTO accounts (account_id, balance, updated_at) VALUES ($1, $2, NOW())`)).
		WithArgs(req.AccountId, newBalance).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.ExpectCommit()

	mockRedis.ExpectXAdd(&redis.XAddArgs{
		Stream: "balance:updated",
		Values: map[string]interface{}{"payload": redismock.AnyValue},
	}).SetVal("some-stream-id")

	ctx := context.Background()
	resp, err := s.CreditAccount(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, req.AccountId, resp.AccountId)
	assert.Equal(t, newBalance, resp.CurrentBalance)

	assert.NoError(t, mockDb.ExpectationsWereMet())
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}
