package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cardprocessingpb "github.com/sambacha/monzo/v2/card_processing/card_processing"
)

// Mock CardProcessingClient
type mockCardProcessingClient struct {
	mock.Mock
}

func (m *mockCardProcessingClient) AuthorizeCardTransaction(ctx context.Context, in *cardprocessingpb.CardAuthRequest, opts ...grpc.CallOption) (*cardprocessingpb.CardAuthReply, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cardprocessingpb.CardAuthReply), args.Error(1)
}

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, *mockCardProcessingClient) {
	mockClient := new(mockCardProcessingClient)
	s := &server{
		cardProcessingClient: mockClient,
	}
	return s, mockClient
}

func TestCardAuthHandler_Approved(t *testing.T) {
	s, mockClient := newTestServer(t)

	requestBody := `{"card_id":"card-123", "amount":1000, "currency":"GBP", "merchant_name":"Test Shop"}`
	expectedGrpcReq := &cardprocessingpb.CardAuthRequest{
		CardId:       "card-123",
		Amount:       1000,
		Currency:     "GBP",
		MerchantName: "Test Shop",
	}
	expectedGrpcResp := &cardprocessingpb.CardAuthReply{Approved: true}

	// Mock AuthorizeCardTransaction call
	mockClient.On("AuthorizeCardTransaction", mock.Anything, expectedGrpcReq).
		Return(expectedGrpcResp, nil).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cardAuth", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := s.cardAuthHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, true, resp["approved"])
	assert.Equal(t, "", resp["reason"]) // Ensure reason is empty on success

	mockClient.AssertExpectations(t)
}

func TestCardAuthHandler_Declined(t *testing.T) {
	s, mockClient := newTestServer(t)

	requestBody := `{"card_id":"card-456", "amount":5000, "currency":"USD", "merchant_name":"Another Shop"}`
	expectedGrpcReq := &cardprocessingpb.CardAuthRequest{
		CardId:       "card-456",
		Amount:       5000,
		Currency:     "USD",
		MerchantName: "Another Shop",
	}
	declineReason := "insufficient funds"
	expectedGrpcResp := &cardprocessingpb.CardAuthReply{Approved: false, DeclineReason: declineReason}

	// Mock AuthorizeCardTransaction call
	mockClient.On("AuthorizeCardTransaction", mock.Anything, expectedGrpcReq).
		Return(expectedGrpcResp, nil).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cardAuth", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := s.cardAuthHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code) // Still OK, but approved: false

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, false, resp["approved"])
	assert.Equal(t, declineReason, resp["reason"])

	mockClient.AssertExpectations(t)
}

func TestCardAuthHandler_GrpcError(t *testing.T) {
	s, mockClient := newTestServer(t)

	requestBody := `{"card_id":"card-789", "amount":2000, "currency":"EUR", "merchant_name":"Error Shop"}`
	expectedGrpcReq := &cardprocessingpb.CardAuthRequest{
		CardId:       "card-789",
		Amount:       2000,
		Currency:     "EUR",
		MerchantName: "Error Shop",
	}
	expectedError := status.Error(codes.Internal, "internal processing error")

	// Mock AuthorizeCardTransaction call to return an error
	mockClient.On("AuthorizeCardTransaction", mock.Anything, expectedGrpcReq).
		Return(nil, expectedError).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cardAuth", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := s.cardAuthHandler(c)

	// Assertions
	assert.NoError(t, err) // Echo handles the error mapping
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Contains(t, resp["error"], "internal server error")

	mockClient.AssertExpectations(t)
}

func TestCardAuthHandler_InvalidBody(t *testing.T) {
	s, _ := newTestServer(t)

	requestBody := `{"invalid json`

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cardAuth", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := s.cardAuthHandler(c)

	// Assertions
	assert.NoError(t, err) // Echo handles binding errors internally
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
