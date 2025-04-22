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

	// Import generated protobuf code for dependent services
	balancepb "github.com/manifoldfinance/disco2/v2/balance/balance"
	cardspb "github.com/manifoldfinance/disco2/v2/cards/cards"
	discopb "github.com/manifoldfinance/disco2/v2/disco_payment_gateway/disco_payment_gateway"
	feedpb "github.com/manifoldfinance/disco2/v2/feed/feed"
	merchantpb "github.com/manifoldfinance/disco2/v2/merchant/merchant"
	transactionspb "github.com/manifoldfinance/disco2/v2/transactions/transactions"
)

// --- Mock Clients (Copied/Adapted from other tests) ---

type mockBalanceClient struct{ mock.Mock }

func (m *mockBalanceClient) GetBalance(ctx context.Context, in *balancepb.AccountID, opts ...grpc.CallOption) (*balancepb.BalanceResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*balancepb.BalanceResponse), args.Error(1)
}

func (m *mockBalanceClient) AuthorizeDebit(ctx context.Context, in *balancepb.AuthorizeDebitRequest, opts ...grpc.CallOption) (*balancepb.DebitResult, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*balancepb.DebitResult), args.Error(1)
}

func (m *mockBalanceClient) CreditAccount(ctx context.Context, in *balancepb.CreditRequest, opts ...grpc.CallOption) (*balancepb.BalanceResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*balancepb.BalanceResponse), args.Error(1)
}

type mockFeedClient struct{ mock.Mock }

func (m *mockFeedClient) AddFeedItem(ctx context.Context, in *feedpb.AddFeedItemRequest, opts ...grpc.CallOption) (*feedpb.FeedItem, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*feedpb.FeedItem), args.Error(1)
}

func (m *mockFeedClient) ListFeedItems(ctx context.Context, in *feedpb.ListFeedItemsRequest, opts ...grpc.CallOption) (*feedpb.FeedItems, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*feedpb.FeedItems), args.Error(1)
}

func (m *mockFeedClient) GetFeedItemsByID(ctx context.Context, in *feedpb.FeedItemIDs, opts ...grpc.CallOption) (*feedpb.FeedItems, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*feedpb.FeedItems), args.Error(1)
}

type mockTransactionsClient struct{ mock.Mock }

func (m *mockTransactionsClient) RecordTransaction(ctx context.Context, in *transactionspb.TransactionInput, opts ...grpc.CallOption) (*transactionspb.Transaction, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*transactionspb.Transaction), args.Error(1)
}

func (m *mockTransactionsClient) GetTransaction(ctx context.Context, in *transactionspb.TransactionQuery, opts ...grpc.CallOption) (*transactionspb.Transaction, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*transactionspb.Transaction), args.Error(1)
}

func (m *mockTransactionsClient) ListTransactions(ctx context.Context, in *transactionspb.TransactionsQuery, opts ...grpc.CallOption) (*transactionspb.TransactionsList, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*transactionspb.TransactionsList), args.Error(1)
}

func (m *mockTransactionsClient) UpdateTransaction(ctx context.Context, in *transactionspb.UpdateTransactionRequest, opts ...grpc.CallOption) (*transactionspb.Transaction, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*transactionspb.Transaction), args.Error(1)
}

type mockMerchantClient struct{ mock.Mock }

func (m *mockMerchantClient) GetMerchant(ctx context.Context, in *merchantpb.MerchantID, opts ...grpc.CallOption) (*merchantpb.MerchantData, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*merchantpb.MerchantData), args.Error(1)
}

func (m *mockMerchantClient) FindOrCreateMerchant(ctx context.Context, in *merchantpb.MerchantQuery, opts ...grpc.CallOption) (*merchantpb.MerchantData, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*merchantpb.MerchantData), args.Error(1)
}

func (m *mockMerchantClient) UpdateMerchant(ctx context.Context, in *merchantpb.UpdateMerchantRequest, opts ...grpc.CallOption) (*merchantpb.MerchantData, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*merchantpb.MerchantData), args.Error(1)
}

type mockCardsClient struct{ mock.Mock }

func (m *mockCardsClient) CreateCard(ctx context.Context, in *cardspb.CreateCardRequest, opts ...grpc.CallOption) (*cardspb.Card, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cardspb.Card), args.Error(1)
}

func (m *mockCardsClient) GetCard(ctx context.Context, in *cardspb.GetCardRequest, opts ...grpc.CallOption) (*cardspb.Card, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cardspb.Card), args.Error(1)
}

func (m *mockCardsClient) UpdateCardStatus(ctx context.Context, in *cardspb.UpdateCardStatusRequest, opts ...grpc.CallOption) (*cardspb.Card, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cardspb.Card), args.Error(1)
}

type mockDiscoClient struct{ mock.Mock }

func (m *mockDiscoClient) CreateSession(ctx context.Context, in *discopb.CreateSessionRequest, opts ...grpc.CallOption) (*discopb.CreateSessionResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discopb.CreateSessionResponse), args.Error(1)
}

func (m *mockDiscoClient) GetSessionById(ctx context.Context, in *discopb.GetSessionByIdRequest, opts ...grpc.CallOption) (*discopb.GetSessionResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discopb.GetSessionResponse), args.Error(1)
}

func (m *mockDiscoClient) ListSessions(ctx context.Context, in *discopb.ListSessionsRequest, opts ...grpc.CallOption) (*discopb.ListSessionsResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discopb.ListSessionsResponse), args.Error(1)
}

func (m *mockDiscoClient) GetSessionByPaymentTransaction(ctx context.Context, in *discopb.GetSessionByPaymentTransactionRequest, opts ...grpc.CallOption) (*discopb.GetSessionResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discopb.GetSessionResponse), args.Error(1)
}

func (m *mockDiscoClient) CreateWallet(ctx context.Context, in *discopb.CreateWalletRequest, opts ...grpc.CallOption) (*discopb.CreateWalletResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discopb.CreateWalletResponse), args.Error(1)
}

func (m *mockDiscoClient) EstimatePaymentAmount(ctx context.Context, in *discopb.EstimatePaymentAmountRequest, opts ...grpc.CallOption) (*discopb.EstimatePaymentAmountResponse, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discopb.EstimatePaymentAmountResponse), args.Error(1)
}

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, *mockBalanceClient, *mockFeedClient, *mockTransactionsClient, *mockMerchantClient, *mockCardsClient, *mockDiscoClient) {
	mockBalance := new(mockBalanceClient)
	mockFeed := new(mockFeedClient)
	mockTxn := new(mockTransactionsClient)
	mockMerchant := new(mockMerchantClient)
	mockCards := new(mockCardsClient)
	mockDisco := new(mockDiscoClient)

	s := &server{
		balanceClient:      mockBalance,
		feedClient:         mockFeed,
		transactionsClient: mockTxn,
		merchantClient:     mockMerchant,
		cardsClient:        mockCards,
		discoClient:        mockDisco,
	}
	return s, mockBalance, mockFeed, mockTxn, mockMerchant, mockCards, mockDisco
}

func TestGetBalanceHandler_Success(t *testing.T) {
	s, mockBalance, _, _, _, _, _ := newTestServer(t)

	accountID := "acc-123"
	expectedBalance := int64(50000)
	expectedResp := &balancepb.BalanceResponse{AccountId: accountID, CurrentBalance: expectedBalance}

	// Mock GetBalance call
	mockBalance.On("GetBalance", mock.Anything, &balancepb.AccountID{AccountId: accountID}).
		Return(expectedResp, nil).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/account/balance/"+accountID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("account_id")
	c.SetParamValues(accountID)

	// Call handler
	err := s.getBalanceHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp balancepb.BalanceResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, expectedResp.AccountId, resp.AccountId)
	assert.Equal(t, expectedResp.CurrentBalance, resp.CurrentBalance)

	mockBalance.AssertExpectations(t)
}

func TestGetBalanceHandler_NotFound(t *testing.T) {
	s, mockBalance, _, _, _, _, _ := newTestServer(t)

	accountID := "acc-unknown"
	expectedError := status.Error(codes.NotFound, "account not found")

	// Mock GetBalance call
	mockBalance.On("GetBalance", mock.Anything, &balancepb.AccountID{AccountId: accountID}).
		Return(nil, expectedError).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/account/balance/"+accountID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("account_id")
	c.SetParamValues(accountID)

	// Call handler
	err := s.getBalanceHandler(c)

	// Assertions
	assert.NoError(t, err) // Echo handles error mapping
	assert.Equal(t, http.StatusNotFound, rec.Code)

	mockBalance.AssertExpectations(t)
}

// --- Feed Handler Tests ---

func TestGetFeedHandler_Success(t *testing.T) {
	s, _, mockFeed, mockTxn, mockMerchant, _, _ := newTestServer(t)

	accountID := "acc-feed-1"
	feedItemID1 := "feed-item-1"
	feedItemID2 := "feed-item-2"
	txnID1 := "txn-1"
	merchantID1 := "merch-1"

	// Mock FeedClient.ListFeedItems
	mockFeedItems := &feedpb.FeedItems{
		Items: []*feedpb.FeedItem{
			{Id: feedItemID1, AccountId: accountID, Type: "TRANSACTION", RefId: txnID1, Timestamp: "2023-01-01T10:00:00Z", Content: "Coffee Shop"},
			{Id: feedItemID2, AccountId: accountID, Type: "INFO", RefId: "", Timestamp: "2023-01-01T09:00:00Z", Content: "Welcome message"},
		},
	}
	mockFeed.On("ListFeedItems", mock.Anything, &feedpb.ListFeedItemsRequest{AccountId: accountID, Limit: 0, BeforeId: ""}).
		Return(mockFeedItems, nil).Once()

	// Mock TransactionsClient.GetTransaction
	mockTxnData := &transactionspb.Transaction{
		Id:          txnID1,
		AccountId:   accountID,
		Amount:      -500,
		Currency:    "GBP",
		Status:      "SETTLED",
		MerchantId:  merchantID1,
		MerchantRaw: "COFFEE SHOP LONDON",
		Category:    "Food & Drink",
	}
	mockTxn.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: txnID1}).
		Return(mockTxnData, nil).Once()

	// Mock MerchantClient.GetMerchant
	mockMerchantData := &merchantpb.MerchantData{
		MerchantId: merchantID1,
		Name:       "Coffee Shop",
		Category:   "Food & Drink",
		LogoUrl:    "http://logo.url/coffee.png",
		Mcc:        5812,
	}
	mockMerchant.On("GetMerchant", mock.Anything, &merchantpb.MerchantID{MerchantId: merchantID1}).
		Return(mockMerchantData, nil).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/feed/"+accountID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("account_id")
	c.SetParamValues(accountID)

	// Call handler
	err := s.getFeedHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Assert response body structure and content
	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Contains(t, resp, "items")
	items, ok := resp["items"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, items, 2)

	// Check enriched transaction item
	item1, ok1 := items[0].(map[string]interface{})
	assert.True(t, ok1)
	assert.Equal(t, feedItemID1, item1["id"])
	assert.Equal(t, "TRANSACTION", item1["type"])
	assert.Contains(t, item1, "transaction")
	txnDetails, okTxn := item1["transaction"].(map[string]interface{})
	assert.True(t, okTxn)
	assert.Equal(t, txnID1, txnDetails["id"])
	assert.Equal(t, float64(-500), txnDetails["amount"]) // JSON numbers are float64
	assert.Contains(t, item1, "merchant")
	merchantDetails, okMerch := item1["merchant"].(map[string]interface{})
	assert.True(t, okMerch)
	assert.Equal(t, merchantID1, merchantDetails["id"])
	assert.Equal(t, "Coffee Shop", merchantDetails["name"])

	// Check info item
	item2, ok2 := items[1].(map[string]interface{})
	assert.True(t, ok2)
	assert.Equal(t, feedItemID2, item2["id"])
	assert.Equal(t, "INFO", item2["type"])
	assert.NotContains(t, item2, "transaction")
	assert.NotContains(t, item2, "merchant")

	// Assert mock calls
	mockFeed.AssertExpectations(t)
	mockTxn.AssertExpectations(t)
	mockMerchant.AssertExpectations(t)
}

func TestGetFeedHandler_FeedError(t *testing.T) {
	s, _, mockFeed, _, _, _, _ := newTestServer(t)

	accountID := "acc-feed-err"
	expectedError := status.Error(codes.Internal, "feed service down")

	// Mock FeedClient.ListFeedItems to return an error
	mockFeed.On("ListFeedItems", mock.Anything, &feedpb.ListFeedItemsRequest{AccountId: accountID, Limit: 0, BeforeId: ""}).
		Return(nil, expectedError).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/feed/"+accountID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("account_id")
	c.SetParamValues(accountID)

	// Call handler
	err := s.getFeedHandler(c)

	// Assertions
	assert.NoError(t, err) // Echo handles the error
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "failed to get feed items")

	mockFeed.AssertExpectations(t)
}

func TestGetFeedHandler_TransactionError(t *testing.T) {
	s, _, mockFeed, mockTxn, _, _, _ := newTestServer(t)

	accountID := "acc-txn-err"
	feedItemID1 := "feed-item-txn-err"
	txnID1 := "txn-err"

	// Mock FeedClient.ListFeedItems
	mockFeedItems := &feedpb.FeedItems{
		Items: []*feedpb.FeedItem{
			{Id: feedItemID1, AccountId: accountID, Type: "TRANSACTION", RefId: txnID1, Timestamp: "2023-01-01T10:00:00Z", Content: "Error Shop"},
		},
	}
	mockFeed.On("ListFeedItems", mock.Anything, &feedpb.ListFeedItemsRequest{AccountId: accountID, Limit: 0, BeforeId: ""}).
		Return(mockFeedItems, nil).Once()

	// Mock TransactionsClient.GetTransaction to return an error
	expectedTxnError := status.Error(codes.NotFound, "transaction not found")
	mockTxn.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: txnID1}).
		Return(nil, expectedTxnError).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/feed/"+accountID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("account_id")
	c.SetParamValues(accountID)

	// Call handler
	err := s.getFeedHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code) // Handler continues even if enrichment fails

	// Assert response body structure and content (item should be present but without enrichment)
	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Contains(t, resp, "items")
	items, ok := resp["items"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, items, 1)
	item1, ok1 := items[0].(map[string]interface{})
	assert.True(t, ok1)
	assert.Equal(t, feedItemID1, item1["id"])
	assert.NotContains(t, item1, "transaction") // Enrichment failed
	assert.NotContains(t, item1, "merchant")

	// Assert mock calls
	mockFeed.AssertExpectations(t)
	mockTxn.AssertExpectations(t)
}

// --- Cards Handlers ---

func TestFreezeCardHandler_Success(t *testing.T) {
	s, _, _, _, _, mockCards, _ := newTestServer(t)

	cardID := "card-to-freeze"
	expectedResp := &cardspb.Card{Id: cardID, UserId: "user-1", Status: "FROZEN", LastFour: "1111"}

	// Mock UpdateCardStatus call
	mockCards.On("UpdateCardStatus", mock.Anything, &cardspb.UpdateCardStatusRequest{CardId: cardID, NewStatus: "FROZEN"}).
		Return(expectedResp, nil).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cards/"+cardID+"/freeze", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(cardID)

	// Call handler
	err := s.freezeCardHandler(c)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp cardspb.Card
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, expectedResp.Id, resp.Id)
	assert.Equal(t, expectedResp.Status, resp.Status)

	mockCards.AssertExpectations(t)
}

func TestFreezeCardHandler_NotFound(t *testing.T) {
	s, _, _, _, _, mockCards, _ := newTestServer(t)

	cardID := "card-not-found"
	expectedError := status.Error(codes.NotFound, "card not found")

	// Mock UpdateCardStatus call
	mockCards.On("UpdateCardStatus", mock.Anything, &cardspb.UpdateCardStatusRequest{CardId: cardID, NewStatus: "FROZEN"}).
		Return(nil, expectedError).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cards/"+cardID+"/freeze", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(cardID)

	// Call handler
	err := s.freezeCardHandler(c)

	// Assertions
	assert.NoError(t, err) // Echo handles error mapping
	assert.Equal(t, http.StatusNotFound, rec.Code)

	mockCards.AssertExpectations(t)
}

func TestFreezeCardHandler_GrpcError(t *testing.T) {
	s, _, _, _, _, mockCards, _ := newTestServer(t)

	cardID := "card-grpc-error"
	expectedError := status.Error(codes.Internal, "internal cards error")

	// Mock UpdateCardStatus call
	mockCards.On("UpdateCardStatus", mock.Anything, &cardspb.UpdateCardStatusRequest{CardId: cardID, NewStatus: "FROZEN"}).
		Return(nil, expectedError).Once()

	// Setup Echo context
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cards/"+cardID+"/freeze", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(cardID)

	// Call handler
	err := s.freezeCardHandler(c)

	// Assertions
	assert.NoError(t, err) // Echo handles error mapping
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	mockCards.AssertExpectations(t)
}

// --- Disco Handlers ---

func TestCreateDiscoSessionHandler_Success(t *testing.T) {
	s, _, _, _, _, _, mockDisco := newTestServer(t)

	requestBody := `{"user_id":"user-disco-1", "currency":"USD", "amount":10000, "redirect_url":"http://example.com/redirect"}`
	expectedGrpcReq := &discopb.CreateSessionRequest{
		UserId:      "user-disco-1",
		Currency:    "USD",
		Amount:      10000,
		RedirectUrl: "http://example.com/redirect",
	}
	expectedGrpcResp := &discopb.CreateSessionResponse{SessionId: "disco-sess-1", Status: "pending", PaymentUrl: "http://disco.pay/1"}

	mockDisco.On("CreateSession", mock.Anything, expectedGrpcReq).Return(expectedGrpcResp, nil).Once()

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/payments/disco/session", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := s.createDiscoSessionHandler(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)
	var resp discopb.CreateSessionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, expectedGrpcResp.SessionId, resp.SessionId)
	assert.Equal(t, expectedGrpcResp.PaymentUrl, resp.PaymentUrl)

	mockDisco.AssertExpectations(t)
}

func TestGetDiscoSessionByIdHandler_Success(t *testing.T) {
	s, _, _, _, _, _, mockDisco := newTestServer(t)

	sessionID := "disco-sess-2"
	expectedGrpcReq := &discopb.GetSessionByIdRequest{SessionId: sessionID}
	expectedGrpcResp := &discopb.GetSessionResponse{SessionId: sessionID, Status: "completed", UserId: "user-disco-2"}

	mockDisco.On("GetSessionById", mock.Anything, expectedGrpcReq).Return(expectedGrpcResp, nil).Once()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/payments/disco/session/"+sessionID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("session_id")
	c.SetParamValues(sessionID)

	err := s.getDiscoSessionByIdHandler(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp discopb.GetSessionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, expectedGrpcResp.SessionId, resp.SessionId)
	assert.Equal(t, expectedGrpcResp.Status, resp.Status)

	mockDisco.AssertExpectations(t)
}

func TestGetDiscoSessionByIdHandler_NotFound(t *testing.T) {
	s, _, _, _, _, _, mockDisco := newTestServer(t)

	sessionID := "disco-sess-notfound"
	expectedGrpcReq := &discopb.GetSessionByIdRequest{SessionId: sessionID}
	expectedError := status.Error(codes.NotFound, "session not found")

	mockDisco.On("GetSessionById", mock.Anything, expectedGrpcReq).Return(nil, expectedError).Once()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/payments/disco/session/"+sessionID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("session_id")
	c.SetParamValues(sessionID)

	err := s.getDiscoSessionByIdHandler(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	mockDisco.AssertExpectations(t)
}

func TestListDiscoSessionsHandler_Success(t *testing.T) {
	s, _, _, _, _, _, mockDisco := newTestServer(t)

	userID := "user-disco-3"
	expectedGrpcReq := &discopb.ListSessionsRequest{UserId: userID, Limit: 0, Status: "", Cursor: ""} // Default values
	expectedGrpcResp := &discopb.ListSessionsResponse{
		Sessions: []*discopb.GetSessionResponse{
			{SessionId: "sess-a", Status: "pending", UserId: userID},
			{SessionId: "sess-b", Status: "completed", UserId: userID},
		},
		NextCursor: "cursor-next",
	}

	mockDisco.On("ListSessions", mock.Anything, expectedGrpcReq).Return(expectedGrpcResp, nil).Once()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/payments/disco/sessions?user_id="+userID, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := s.listDiscoSessionsHandler(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp discopb.ListSessionsResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp.Sessions, 2)
	assert.Equal(t, expectedGrpcResp.NextCursor, resp.NextCursor)

	mockDisco.AssertExpectations(t)
}

func TestEstimateDiscoPaymentAmountHandler_Success(t *testing.T) {
	s, _, _, _, _, _, mockDisco := newTestServer(t)

	requestBody := `{"target_currency":"USD", "target_amount":10000, "source_currency":"ETH"}`
	expectedGrpcReq := &discopb.EstimatePaymentAmountRequest{
		TargetCurrency: "USD",
		TargetAmount:   10000,
		SourceCurrency: "ETH",
	}
	expectedGrpcResp := &discopb.EstimatePaymentAmountResponse{EstimatedAmount: 50000000000000000, SourceCurrency: "ETH"} // Example ETH amount in wei

	mockDisco.On("EstimatePaymentAmount", mock.Anything, expectedGrpcReq).Return(expectedGrpcResp, nil).Once()

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/payments/disco/estimate", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := s.estimateDiscoPaymentAmountHandler(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp discopb.EstimatePaymentAmountResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, expectedGrpcResp.EstimatedAmount, resp.EstimatedAmount)
	assert.Equal(t, expectedGrpcResp.SourceCurrency, resp.SourceCurrency)

	mockDisco.AssertExpectations(t)
}

func TestCreateDiscoWalletHandler_Success(t *testing.T) {
	s, _, _, _, _, _, mockDisco := newTestServer(t)

	requestBody := `{"user_id":"user-disco-wallet"}`
	expectedGrpcReq := &discopb.CreateWalletRequest{UserId: "user-disco-wallet"}
	expectedGrpcResp := &discopb.CreateWalletResponse{WalletAddress: "0x123abc"}

	mockDisco.On("CreateWallet", mock.Anything, expectedGrpcReq).Return(expectedGrpcResp, nil).Once()

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/payments/disco/wallet", strings.NewReader(requestBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := s.createDiscoWalletHandler(c)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)
	var resp discopb.CreateWalletResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, expectedGrpcResp.WalletAddress, resp.WalletAddress)

	mockDisco.AssertExpectations(t)
}
