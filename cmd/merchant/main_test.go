package main

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	merchantpb "github.com/manifoldfinance/disco2/v2/merchant/merchant"
)

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, sqlmock.Sqlmock, redismock.ClientMock) {
	db, mockDb, err := sqlmock.New()
	assert.NoError(t, err)

	redisClient, mockRedisClient := redismock.NewClientMock()

	s := &server{
		db:          db,
		redisClient: redisClient,
	}
	return s, mockDb, mockRedisClient
}

func TestGetMerchant_Found(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &merchantpb.MerchantID{MerchantId: "merch-123"}
	expectedMerchant := &merchantpb.MerchantData{
		MerchantId: req.MerchantId,
		Name:       "Test Merchant",
		Category:   "Test Category",
		LogoUrl:    "http://example.com/logo.png",
		Mcc:        1234,
	}

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT merchant_id, name, category, logo_url, mcc FROM merchants WHERE merchant_id = $1`)).
		WithArgs(req.MerchantId).
		WillReturnRows(sqlmock.NewRows([]string{"merchant_id", "name", "category", "logo_url", "mcc"}).
			AddRow(expectedMerchant.MerchantId, expectedMerchant.Name, sql.NullString{String: expectedMerchant.Category, Valid: true}, sql.NullString{String: expectedMerchant.LogoUrl, Valid: true}, sql.NullInt32{Int32: expectedMerchant.Mcc, Valid: true}))

	ctx := context.Background()
	resp, err := s.GetMerchant(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expectedMerchant, resp)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestGetMerchant_NotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &merchantpb.MerchantID{MerchantId: "merch-unknown"}

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT merchant_id, name, category, logo_url, mcc FROM merchants WHERE merchant_id = $1`)).
		WithArgs(req.MerchantId).
		WillReturnError(sql.ErrNoRows)

	ctx := context.Background()
	resp, err := s.GetMerchant(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestFindOrCreateMerchant_Found(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &merchantpb.MerchantQuery{RawName: "Existing Merchant", Mcc: 5678}
	expectedMerchant := &merchantpb.MerchantData{
		MerchantId: "merch-found",
		Name:       "Existing Merchant",
		Category:   "Found Category",
		LogoUrl:    "",
		Mcc:        5678,
	}

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT merchant_id, name, category, logo_url, mcc FROM merchants WHERE lower(name) = lower($1) AND coalesce(mcc, 0) = coalesce($2, 0)`)).
		WithArgs(req.RawName, sql.NullInt32{Int32: req.Mcc, Valid: true}).
		WillReturnRows(sqlmock.NewRows([]string{"merchant_id", "name", "category", "logo_url", "mcc"}).
			AddRow(expectedMerchant.MerchantId, expectedMerchant.Name, sql.NullString{String: expectedMerchant.Category, Valid: true}, sql.NullString{}, sql.NullInt32{Int32: expectedMerchant.Mcc, Valid: true}))

	ctx := context.Background()
	resp, err := s.FindOrCreateMerchant(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expectedMerchant, resp)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestFindOrCreateMerchant_Create(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &merchantpb.MerchantQuery{RawName: "New Merchant", Mcc: 9012}
	expectedName := "New Merchant" // After strings.Title(strings.ToLower(...))

	// Mock lookup query (returns not found)
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT merchant_id, name, category, logo_url, mcc FROM merchants WHERE lower(name) = lower($1) AND coalesce(mcc, 0) = coalesce($2, 0)`)).
		WithArgs(req.RawName, sql.NullInt32{Int32: req.Mcc, Valid: true}).
		WillReturnError(sql.ErrNoRows)

	// Mock insert query
	mockDb.ExpectQuery(regexp.QuoteMeta(`INSERT INTO merchants (merchant_id, name, category, mcc, created_at, updated_at) VALUES ($1, $2, $3, $4, NOW(), NOW()) RETURNING merchant_id, name, category, logo_url, mcc`)).
		WithArgs(sqlmock.AnyArg(), expectedName, sql.NullString{Valid: false}, sql.NullInt32{Int32: req.Mcc, Valid: true}).
		WillReturnRows(sqlmock.NewRows([]string{"merchant_id", "name", "category", "logo_url", "mcc"}).
			AddRow("new-merch-id", expectedName, sql.NullString{}, sql.NullString{}, sql.NullInt32{Int32: req.Mcc, Valid: true}))

	ctx := context.Background()
	resp, err := s.FindOrCreateMerchant(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "new-merch-id", resp.MerchantId)
	assert.Equal(t, expectedName, resp.Name)
	assert.Equal(t, req.Mcc, resp.Mcc)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestUpdateMerchant_Success(t *testing.T) {
	s, mockDb, mockRedis := newTestServer(t)
	defer s.db.Close()

	req := &merchantpb.UpdateMerchantRequest{
		MerchantId: "merch-to-update",
		Name:       "Updated Name",
		Category:   "Updated Category",
		LogoUrl:    "http://new.logo/url.png",
		Mcc:        1111,
	}

	// Mock DB UPDATE query
	mockDb.ExpectQuery(`UPDATE merchants SET name = \$1, category = \$2, logo_url = \$3, mcc = \$4, updated_at = \$5 WHERE merchant_id = \$6 RETURNING merchant_id, name, category, logo_url, mcc`).
		WithArgs(req.Name, req.Category, req.LogoUrl, req.Mcc, sqlmock.AnyArg(), req.MerchantId).
		WillReturnRows(sqlmock.NewRows([]string{"merchant_id", "name", "category", "logo_url", "mcc"}).
			AddRow(req.MerchantId, req.Name, sql.NullString{String: req.Category, Valid: true}, sql.NullString{String: req.LogoUrl, Valid: true}, sql.NullInt32{Int32: req.Mcc, Valid: true}))

	// Mock Redis XAdd command with any payload value
	eventPayload := `{"merchant_id": "merch-to-update", "name": "Updated Name", "category": "Updated Category", "logo_url": "http://new.logo/url.png", "mcc": 1111}`
	mockRedis.ExpectXAdd(&redis.XAddArgs{
		Stream: "merchant:updated",
		Values: map[string]interface{}{
			"payload": eventPayload,
		},
	}).SetVal("some-stream-id")

	ctx := context.Background()
	resp, err := s.UpdateMerchant(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, req.MerchantId, resp.MerchantId)
	assert.Equal(t, req.Name, resp.Name)
	assert.Equal(t, req.Category, resp.Category)
	assert.Equal(t, req.LogoUrl, resp.LogoUrl)
	assert.Equal(t, req.Mcc, resp.Mcc)

	assert.NoError(t, mockDb.ExpectationsWereMet())
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestUpdateMerchant_NotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t) // No Redis mock needed if DB fails
	defer s.db.Close()

	req := &merchantpb.UpdateMerchantRequest{
		MerchantId: "merch-unknown",
		Name:       "Updated Name",
	}

	// Mock DB UPDATE query to return no rows
	mockDb.ExpectQuery(`UPDATE merchants SET name = \$1, updated_at = \$2 WHERE merchant_id = \$3 RETURNING merchant_id, name, category, logo_url, mcc`).
		WithArgs(req.Name, sqlmock.AnyArg(), req.MerchantId).
		WillReturnError(sql.ErrNoRows)

	ctx := context.Background()
	resp, err := s.UpdateMerchant(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestUpdateMerchant_NoFields(t *testing.T) {
	s, _, _ := newTestServer(t) // No DB/Redis interaction expected
	defer s.db.Close()

	req := &merchantpb.UpdateMerchantRequest{
		MerchantId: "merch-no-fields",
		// No fields to update
	}

	ctx := context.Background()
	resp, err := s.UpdateMerchant(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
