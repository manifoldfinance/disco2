package main

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-redis/redismock/v8"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cardspb "github.com/manifoldfinance/disco2/v2/cards/cards"
)

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, sqlmock.Sqlmock, redismock.ClientMock) {
	db, mockDb, err := sqlmock.New()
	assert.NoError(t, err)

	mockRedis, mockRedisClient := redismock.NewClientMock()

	s := &server{
		db:          db,
		redisClient: mockRedisClient,
	}
	return s, mockDb, mockRedisClient
}

func TestCreateCard(t *testing.T) {
	s, mockDb, mockRedis := newTestServer(t)
	defer s.db.Close()

	req := &cardspb.CreateCardRequest{
		UserId:   "user-123",
		CardType: "virtual",
	}

	// Mock DB INSERT query
	// Use regexp matching for the query because UUID is generated dynamically
	mockDb.ExpectQuery(regexp.QuoteMeta(`INSERT INTO cards (card_id, user_id, status, created_at) VALUES ($1, $2, $3, NOW()) RETURNING card_id, user_id, status, last_four, created_at, updated_at`)).
		WithArgs(sqlmock.AnyArg(), req.UserId, "ACTIVE"). // Check user_id and status
		WillReturnRows(sqlmock.NewRows([]string{"card_id", "user_id", "status", "last_four", "created_at", "updated_at"}).
			AddRow("new-card-id", req.UserId, "ACTIVE", sql.NullString{}, time.Now(), sql.NullTime{}))

	// Mock Redis XAdd command
	mockRedis.ExpectXAdd(&redis.XAddArgs{
		Stream: "card:created",
		Values: map[string]interface{}{"payload": redismock.AnyValue}, // Check stream name, ignore payload content for simplicity
	}).SetVal("some-stream-id")

	ctx := context.Background()
	resp, err := s.CreateCard(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "new-card-id", resp.Id)
	assert.Equal(t, req.UserId, resp.UserId)
	assert.Equal(t, "ACTIVE", resp.Status)

	// Verify that all expectations were met
	assert.NoError(t, mockDb.ExpectationsWereMet())
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestGetCard_Found(t *testing.T) {
	s, mockDb, _ := newTestServer(t) // No Redis mock needed for GetCard
	defer s.db.Close()

	req := &cardspb.GetCardRequest{CardId: "card-abc"}

	// Mock DB SELECT query
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT card_id, user_id, status, last_four FROM cards WHERE card_id = $1`)).
		WithArgs(req.CardId).
		WillReturnRows(sqlmock.NewRows([]string{"card_id", "user_id", "status", "last_four"}).
			AddRow(req.CardId, "user-123", "ACTIVE", "1234"))

	ctx := context.Background()
	resp, err := s.GetCard(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, req.CardId, resp.Id)
	assert.Equal(t, "user-123", resp.UserId)
	assert.Equal(t, "ACTIVE", resp.Status)
	assert.Equal(t, "1234", resp.LastFour)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestGetCard_NotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	req := &cardspb.GetCardRequest{CardId: "card-xyz"}

	// Mock DB SELECT query to return no rows
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT card_id, user_id, status, last_four FROM cards WHERE card_id = $1`)).
		WithArgs(req.CardId).
		WillReturnError(sql.ErrNoRows)

	ctx := context.Background()
	resp, err := s.GetCard(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestUpdateCardStatus_Success(t *testing.T) {
	s, mockDb, mockRedis := newTestServer(t)
	defer s.db.Close()

	req := &cardspb.UpdateCardStatusRequest{
		CardId:    "card-def",
		NewStatus: "FROZEN",
	}

	// Mock DB UPDATE query
	mockDb.ExpectQuery(regexp.QuoteMeta(`UPDATE cards SET status = $1, updated_at = NOW() WHERE card_id = $2 RETURNING card_id, user_id, status, last_four`)).
		WithArgs(req.NewStatus, req.CardId).
		WillReturnRows(sqlmock.NewRows([]string{"card_id", "user_id", "status", "last_four"}).
			AddRow(req.CardId, "user-456", req.NewStatus, "5678"))

	// Mock Redis XAdd command
	mockRedis.ExpectXAdd(&redis.XAddArgs{
		Stream: "card:status_changed",
		Values: map[string]interface{}{"payload": redismock.AnyValue},
	}).SetVal("some-stream-id")

	ctx := context.Background()
	resp, err := s.UpdateCardStatus(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, req.CardId, resp.Id)
	assert.Equal(t, "user-456", resp.UserId)
	assert.Equal(t, req.NewStatus, resp.Status)
	assert.Equal(t, "5678", resp.LastFour)

	assert.NoError(t, mockDb.ExpectationsWereMet())
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestUpdateCardStatus_InvalidStatus(t *testing.T) {
	s, _, _ := newTestServer(t) // No DB/Redis interaction expected
	defer s.db.Close()

	req := &cardspb.UpdateCardStatusRequest{
		CardId:    "card-ghi",
		NewStatus: "INVALID_STATUS",
	}

	ctx := context.Background()
	resp, err := s.UpdateCardStatus(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateCardStatus_NotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t) // No Redis mock needed if DB fails
	defer s.db.Close()

	req := &cardspb.UpdateCardStatusRequest{
		CardId:    "card-jkl",
		NewStatus: "FROZEN",
	}

	// Mock DB UPDATE query to return no rows
	mockDb.ExpectQuery(regexp.QuoteMeta(`UPDATE cards SET status = $1, updated_at = NOW() WHERE card_id = $2 RETURNING card_id, user_id, status, last_four`)).
		WithArgs(req.NewStatus, req.CardId).
		WillReturnError(sql.ErrNoRows)

	ctx := context.Background()
	resp, err := s.UpdateCardStatus(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	assert.NoError(t, mockDb.ExpectationsWereMet())
}
