package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	transactionspb "github.com/sambacha/monzo/v2/transactions/transactions"
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

func TestRecordTransaction(t *testing.T) {
	s, mockDb, mockRedis := newTestServer(t)
	defer s.db.Close()

	now := time.Now()
	req := &transactionspb.TransactionInput{
		AccountId:   "acc-123",
		CardId:      "card-abc",
		Amount:      12345,
		Currency:    "GBP",
		MerchantRaw: "Test Merchant",
		Status:      "AUTHORIZED",
	}

	// Mock DB INSERT query
	mockDb.ExpectQuery(regexp.QuoteMeta(`INSERT INTO transactions (id, account_id, card_id, amount, currency, merchant_id, merchant_raw, status, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, account_id, card_id, amount, currency, merchant_id, merchant_raw, status, created_at`)).
		WithArgs(sqlmock.AnyArg(), req.AccountId, sql.NullString{String: req.CardId, Valid: true}, req.Amount, req.Currency, sql.NullString{Valid: false}, sql.NullString{String: req.MerchantRaw, Valid: true}, req.Status, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "card_id", "amount", "currency", "merchant_id", "merchant_raw", "status", "created_at"}).
			AddRow("txn-xyz", req.AccountId, sql.NullString{String: req.CardId, Valid: true}, req.Amount, req.Currency, sql.NullString{Valid: false}, sql.NullString{String: req.MerchantRaw, Valid: true}, req.Status, now))

	// Mock Redis XAdd command
	mockRedis.ExpectXAdd(&redis.XAddArgs{
		Stream: "transaction:created",
		Values: map[string]interface{}{"payload": redismock.AnyValue},
	}).SetVal("some-stream-id")

	ctx := context.Background()
	resp, err := s.RecordTransaction(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "txn-xyz", resp.Id)
	assert.Equal(t, req.AccountId, resp.AccountId)
	assert.Equal(t, req.CardId, resp.CardId)
	assert.Equal(t, req.Amount, resp.Amount)
	assert.Equal(t, req.Currency, resp.Currency)
	assert.Equal(t, req.MerchantRaw, resp.MerchantRaw)
	assert.Equal(t, req.Status, resp.Status)
	assert.Equal(t, now.Format(time.RFC3339), resp.Timestamp)

	assert.NoError(t, mockDb.ExpectationsWereMet())
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestGetTransaction_Found(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	now := time.Now()
	req := &transactionspb.TransactionQuery{Id: "txn-abc"}
	expectedTxn := &transactionspb.Transaction{
		Id:          req.Id,
		AccountId:   "acc-123",
		CardId:      "card-abc",
		Amount:      5000,
		Currency:    "GBP",
		MerchantId:  "merch-xyz",
		MerchantRaw: "Raw Merchant",
		Category:    "Groceries",
		Status:      "SETTLED",
		Timestamp:   now.Format(time.RFC3339),
	}

	// Mock DB SELECT query
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, card_id, amount, currency, merchant_id, merchant_raw, category, status, created_at FROM transactions WHERE id = $1`)).
		WithArgs(req.Id).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "card_id", "amount", "currency", "merchant_id", "merchant_raw", "category", "status", "created_at"}).
			AddRow(expectedTxn.Id, expectedTxn.AccountId, sql.NullString{String: expectedTxn.CardId, Valid: true}, expectedTxn.Amount, expectedTxn.Currency, sql.NullString{String: expectedTxn.MerchantId, Valid: true}, sql.NullString{String: expectedTxn.MerchantRaw, Valid: true}, sql.NullString{String: expectedTxn.Category, Valid: true}, expectedTxn.Status, now))

	ctx := context.Background()
	resp, err := s.GetTransaction(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expectedTxn, resp)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestGetTransaction_NotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &transactionspb.TransactionQuery{Id: "txn-unknown"}

	// Mock DB SELECT query to return no rows
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, card_id, amount, currency, merchant_id, merchant_raw, category, status, created_at FROM transactions WHERE id = $1`)).
		WithArgs(req.Id).
		WillReturnError(sql.ErrNoRows)

	ctx := context.Background()
	resp, err := s.GetTransaction(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestListTransactions(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	now := time.Now()
	req := &transactionspb.TransactionsQuery{AccountId: "acc-123", Limit: 10}

	// Mock DB SELECT query
	rows := sqlmock.NewRows([]string{"id", "account_id", "card_id", "amount", "currency", "merchant_id", "merchant_raw", "category", "status", "created_at"}).
		AddRow("txn-1", req.AccountId, sql.NullString{String: "card-abc", Valid: true}, 5000, "GBP", sql.NullString{}, sql.NullString{String: "Merchant 1", Valid: true}, sql.NullString{}, "SETTLED", now.Add(-1*time.Hour)).
		AddRow("txn-2", req.AccountId, sql.NullString{String: "card-abc", Valid: true}, 2500, "GBP", sql.NullString{}, sql.NullString{String: "Merchant 2", Valid: true}, sql.NullString{}, "AUTHORIZED", now)

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, card_id, amount, currency, merchant_id, merchant_raw, category, status, created_at FROM transactions WHERE account_id = $1 ORDER BY created_at DESC LIMIT 10`)).
		WithArgs(req.AccountId).
		WillReturnRows(rows)

	ctx := context.Background()
	resp, err := s.ListTransactions(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Items, 2)
	assert.Equal(t, "txn-2", resp.Items[0].Id) // Ensure descending order
	assert.Equal(t, "txn-1", resp.Items[1].Id)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestUpdateTransaction(t *testing.T) {
	s, mockDb, _ := newTestServer(t) // No Redis mock needed for Update
	defer s.db.Close()

	now := time.Now()
	req := &transactionspb.UpdateTransactionRequest{
		Id:           "txn-abc",
		MerchantId:   "merch-xyz",
		MerchantName: "Clean Merchant Name",
		Category:     "Dining",
		Status:       "SETTLED",
	}

	// Mock DB UPDATE query
	// Note: The query is built dynamically, so matching exactly is tricky.
	// We'll match the core part and check arguments.
	mockDb.ExpectQuery(`UPDATE transactions SET merchant_id = \$1, merchant_name = \$2, category = \$3, status = \$4 WHERE id = \$5 RETURNING id, account_id, card_id, amount, currency, merchant_id, merchant_raw, category, status, created_at`). // Use regex for flexibility
																															WithArgs(req.MerchantId, req.MerchantName, req.Category, req.Status, req.Id).
																															WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "card_id", "amount", "currency", "merchant_id", "merchant_raw", "category", "status", "created_at"}).
																																AddRow(req.Id, "acc-123", sql.NullString{String: "card-abc", Valid: true}, 5000, "GBP", sql.NullString{String: req.MerchantId, Valid: true}, sql.NullString{String: "Raw Name", Valid: true}, sql.NullString{String: req.Category, Valid: true}, req.Status, now))

	ctx := context.Background()
	resp, err := s.UpdateTransaction(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, req.Id, resp.Id)
	assert.Equal(t, req.MerchantId, resp.MerchantId)
	assert.Equal(t, req.Category, resp.Category)
	assert.Equal(t, req.Status, resp.Status)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}
