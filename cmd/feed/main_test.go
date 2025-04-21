package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	feedpb "github.com/manifoldfinance/disco2/v2/feed/feed"
)

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, sqlmock.Sqlmock) {
	db, mockDb, err := sqlmock.New()
	assert.NoError(t, err)

	// No Redis client needed for feed service tests based on current implementation
	s := &server{
		db: db,
	}
	return s, mockDb
}

func TestAddFeedItem(t *testing.T) {
	s, mockDb := newTestServer(t)
	defer s.db.Close()

	now := time.Now()
	req := &feedpb.AddFeedItemRequest{
		AccountId: "acc-123",
		Type:      "TRANSACTION",
		Content:   "Spent 10.00 GBP at Test",
		RefId:     "txn-abc",
		Timestamp: now.Format(time.RFC3339),
	}

	// Mock DB INSERT query
	mockDb.ExpectQuery(regexp.QuoteMeta(`INSERT INTO feed_items (id, account_id, type, content, ref_id, timestamp) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, account_id, type, content, ref_id, timestamp`)).
		WithArgs(sqlmock.AnyArg(), req.AccountId, req.Type, sql.NullString{String: req.Content, Valid: true}, sql.NullString{String: req.RefId, Valid: true}, sqlmock.AnyArg()). // Check args, allow any timestamp/id
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "type", "content", "ref_id", "timestamp"}).
			AddRow("feed-xyz", req.AccountId, req.Type, sql.NullString{String: req.Content, Valid: true}, sql.NullString{String: req.RefId, Valid: true}, now))

	ctx := context.Background()
	resp, err := s.AddFeedItem(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "feed-xyz", resp.Id)
	assert.Equal(t, req.AccountId, resp.AccountId)
	assert.Equal(t, req.Type, resp.Type)
	assert.Equal(t, req.Content, resp.Content)
	assert.Equal(t, req.RefId, resp.RefId)
	assert.Equal(t, now.Format(time.RFC3339), resp.Timestamp)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestListFeedItems(t *testing.T) {
	s, mockDb := newTestServer(t)
	defer s.db.Close()

	now := time.Now()
	req := &feedpb.ListFeedItemsRequest{AccountId: "acc-123", Limit: 10}

	// Mock DB SELECT query
	rows := sqlmock.NewRows([]string{"id", "account_id", "type", "content", "ref_id", "timestamp"}).
		AddRow("feed-2", req.AccountId, "TRANSACTION", sql.NullString{String: "Content 2", Valid: true}, sql.NullString{String: "txn-2", Valid: true}, now).
		AddRow("feed-1", req.AccountId, "MESSAGE", sql.NullString{String: "Content 1", Valid: true}, sql.NullString{}, now.Add(-1*time.Hour))

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, type, content, ref_id, timestamp FROM feed_items WHERE account_id = $1 ORDER BY timestamp DESC LIMIT $2`)).
		WithArgs(req.AccountId, req.Limit).
		WillReturnRows(rows)

	ctx := context.Background()
	resp, err := s.ListFeedItems(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Items, 2)
	assert.Equal(t, "feed-2", resp.Items[0].Id) // Ensure descending order
	assert.Equal(t, "feed-1", resp.Items[1].Id)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestGetFeedItemsByID_Found(t *testing.T) {
	s, mockDb := newTestServer(t)
	defer s.db.Close()

	now := time.Now()
	req := &feedpb.FeedItemIDs{Ids: []string{"feed-abc", "feed-def"}}

	// Mock DB SELECT query
	rows := sqlmock.NewRows([]string{"id", "account_id", "type", "content", "ref_id", "timestamp"}).
		AddRow("feed-abc", "acc-1", "TYPE_A", sql.NullString{String: "Content A", Valid: true}, sql.NullString{}, now).
		AddRow("feed-def", "acc-2", "TYPE_B", sql.NullString{String: "Content B", Valid: true}, sql.NullString{String: "ref-b", Valid: true}, now.Add(-5*time.Minute))

	// Note: The order of IDs in the IN clause might vary, so using AnyArgs() or a more flexible regex might be needed in complex cases.
	// For simplicity, assuming the order matches req.Ids for this test.
	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, type, content, ref_id, timestamp FROM feed_items WHERE id IN ($1,$2) ORDER BY timestamp DESC`)).
		WithArgs(req.Ids[0], req.Ids[1]).
		WillReturnRows(rows)

	ctx := context.Background()
	resp, err := s.GetFeedItemsByID(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Items, 2)
	// Check if the returned items match the expected IDs (order might vary based on timestamp)
	foundIDs := make(map[string]bool)
	for _, item := range resp.Items {
		foundIDs[item.Id] = true
	}
	assert.True(t, foundIDs["feed-abc"])
	assert.True(t, foundIDs["feed-def"])

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestGetFeedItemsByID_EmptyInput(t *testing.T) {
	s, _ := newTestServer(t) // No DB interaction expected
	defer s.db.Close()

	req := &feedpb.FeedItemIDs{Ids: []string{}}

	ctx := context.Background()
	resp, err := s.GetFeedItemsByID(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Items)
}
