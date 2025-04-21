package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-redis/redismock/v8"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	// feedpb "github.com/manifoldfinance/disco2/v2/feed/feed" // Import if needed for consumer test
)

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, sqlmock.Sqlmock, redismock.ClientMock) {
	db, mockDb, err := sqlmock.New()
	assert.NoError(t, err)

	redisClient, mockRedisClient := redismock.NewClientMock()

	// Mock Feed client if needed for consumer tests
	// mockFeedClient := &mockFeedClient{}

	s := &server{
		db:          db,
		redisClient: redisClient,
		// feedClient: mockFeedClient,
	}
	return s, mockDb, mockRedisClient
}

func TestRegisterDeviceHandler_Success(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	userID := "user-123"
	token := "test-device-token"
	requestBody := `{"user_id":"` + userID + `","token":"` + token + `"}`

	// Mock DB INSERT query
	mockDb.ExpectQuery(regexp.QuoteMeta(`INSERT INTO devices (user_id, token, created_at) VALUES ($1, $2, NOW()) ON CONFLICT (user_id, token) DO UPDATE SET created_at = NOW() RETURNING device_id, user_id, token, created_at`)).
		WithArgs(userID, token).
		WillReturnRows(sqlmock.NewRows([]string{"device_id", "user_id", "token", "created_at"}).
			AddRow(1, userID, token, time.Now()))

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/devices", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := s.registerDeviceHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, float64(1), resp["device_id"]) // JSON numbers are float64
	assert.Equal(t, userID, resp["user_id"])
	assert.Equal(t, token, resp["token"])

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestRegisterDeviceHandler_InvalidBody(t *testing.T) {
	s, _, _ := newTestServer(t)
	defer s.db.Close()

	requestBody := `{"invalid json`

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/devices", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := s.registerDeviceHandler(c)

	// Assertions
	assert.NoError(t, err) // Echo handles binding errors internally
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterDeviceHandler_MissingFields(t *testing.T) {
	s, _, _ := newTestServer(t)
	defer s.db.Close()

	requestBody := `{"user_id":"user-123"}` // Missing token

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/devices", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := s.registerDeviceHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TODO: Add tests for startEventConsumer and helper functions (getDeviceTokensForUser, sendAPNSNotification)
// Testing startEventConsumer directly is complex due to the infinite loop.
// It's often better to test the helper functions it calls.

func TestGetDeviceTokensForUser_Found(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	userID := "user-with-tokens"
	expectedTokens := []string{"token1", "token2"}

	rows := sqlmock.NewRows([]string{"token"}).
		AddRow(expectedTokens[0]).
		AddRow(expectedTokens[1])

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT token FROM devices WHERE user_id = $1`)).
		WithArgs(userID).
		WillReturnRows(rows)

	ctx := context.Background()
	tokens, err := s.getDeviceTokensForUser(ctx, userID)

	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedTokens, tokens) // Use ElementsMatch for slice comparison regardless of order

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

func TestGetDeviceTokensForUser_NotFound(t *testing.T) {
	s, mockDb, _ := newTestServer(t)
	defer s.db.Close()

	userID := "user-without-tokens"

	rows := sqlmock.NewRows([]string{"token"}) // No rows added

	mockDb.ExpectQuery(regexp.QuoteMeta(`SELECT token FROM devices WHERE user_id = $1`)).
		WithArgs(userID).
		WillReturnRows(rows)

	ctx := context.Background()
	tokens, err := s.getDeviceTokensForUser(ctx, userID)

	assert.NoError(t, err)
	assert.Empty(t, tokens)

	assert.NoError(t, mockDb.ExpectationsWereMet())
}

// Test for sendAPNSNotification is trivial as it's currently a placeholder
func TestSendAPNSNotification(t *testing.T) {
	s, _, _ := newTestServer(t)
	defer s.db.Close()

	ctx := context.Background()
	err := s.sendAPNSNotification(ctx, "test-token", "test message")
	assert.NoError(t, err)
}
