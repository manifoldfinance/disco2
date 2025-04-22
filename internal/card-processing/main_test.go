package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	balancepb "github.com/sambacha/monzo/v2/balance/balance"
	cardprocessingpb "github.com/sambacha/monzo/v2/card_processing/card_processing"
	cardspb "github.com/sambacha/monzo/v2/cards/cards"
	transactionspb "github.com/sambacha/monzo/v2/transactions/transactions"
)

// Mock CardsClient
type mockCardsClient struct{ mock.Mock }

func (m *mockCardsClient) CreateCard(ctx context.Context, in *cardspb.CreateCardRequest, opts ...grpc.CallOption) (*cardspb.Card, error) {
	args := m.Called(ctx, in)
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
	return args.Get(0).(*cardspb.Card), args.Error(1)
}

// Mock BalanceClient
type mockBalanceClient struct{ mock.Mock }

func (m *mockBalanceClient) GetBalance(ctx context.Context, in *balancepb.AccountID, opts ...grpc.CallOption) (*balancepb.BalanceResponse, error) {
	args := m.Called(ctx, in)
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
	return args.Get(0).(*balancepb.BalanceResponse), args.Error(1)
}

// Mock TransactionsClient (copied from previous tests)
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
	return args.Get(0).(*transactionspb.TransactionsList), args.Error(1)
}

func (m *mockTransactionsClient) UpdateTransaction(ctx context.Context, in *transactionspb.UpdateTransactionRequest, opts ...grpc.CallOption) (*transactionspb.Transaction, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*transactionspb.Transaction), args.Error(1)
}

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, *mockCardsClient, *mockBalanceClient, *mockTransactionsClient) {
	mockCards := new(mockCardsClient)
	mockBalance := new(mockBalanceClient)
	mockTxn := new(mockTransactionsClient)

	s := &server{
		cardsClient:        mockCards,
		balanceClient:      mockBalance,
		transactionsClient: mockTxn,
	}
	return s, mockCards, mockBalance, mockTxn
}

func TestAuthorizeCardTransaction_Success(t *testing.T) {
	s, mockCards, mockBalance, mockTxn := newTestServer(t)

	req := &cardprocessingpb.CardAuthRequest{
		CardId:       "card-123",
		Amount:       1000,
		Currency:     "GBP",
		MerchantName: "Test Shop",
	}
	userID := "user-abc"

	// Mock GetCard call
	mockCards.On("GetCard", mock.Anything, &cardspb.GetCardRequest{CardId: req.CardId}).
		Return(&cardspb.Card{Id: req.CardId, UserId: userID, Status: "ACTIVE"}, nil).Once()

	// Mock AuthorizeDebit call
	mockBalance.On("AuthorizeDebit", mock.Anything, &balancepb.AuthorizeDebitRequest{AccountId: userID, Amount: req.Amount}).
		Return(&balancepb.DebitResult{Success: true, NewBalance: 9000}, nil).Once()

	// Mock RecordTransaction call
	expectedTxnInput := &transactionspb.TransactionInput{
		AccountId:   userID,
		CardId:      req.CardId,
		Amount:      req.Amount,
		Currency:    req.Currency,
		MerchantId:  req.MerchantId,
		MerchantRaw: req.MerchantName,
		Status:      "AUTHORIZED",
	}
	mockTxn.On("RecordTransaction", mock.Anything, expectedTxnInput).
		Return(&transactionspb.Transaction{Id: "txn-xyz"}, nil).Once()

	ctx := context.Background()
	resp, err := s.AuthorizeCardTransaction(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Approved)
	assert.Empty(t, resp.DeclineReason)

	mockCards.AssertExpectations(t)
	mockBalance.AssertExpectations(t)
	mockTxn.AssertExpectations(t)
}

func TestAuthorizeCardTransaction_CardNotFound(t *testing.T) {
	s, mockCards, mockBalance, mockTxn := newTestServer(t)

	req := &cardprocessingpb.CardAuthRequest{CardId: "card-unknown"}

	// Mock GetCard call to return NotFound
	mockCards.On("GetCard", mock.Anything, &cardspb.GetCardRequest{CardId: req.CardId}).
		Return(nil, status.Error(codes.NotFound, "card not found")).Once()

	ctx := context.Background()
	resp, err := s.AuthorizeCardTransaction(ctx, req)

	assert.NoError(t, err) // Business decline, not gRPC error
	assert.NotNil(t, resp)
	assert.False(t, resp.Approved)
	assert.Equal(t, "card not found", resp.DeclineReason)

	mockCards.AssertExpectations(t)
	mockBalance.AssertNotCalled(t, "AuthorizeDebit", mock.Anything, mock.Anything)
	mockTxn.AssertNotCalled(t, "RecordTransaction", mock.Anything, mock.Anything)
}

func TestAuthorizeCardTransaction_CardInactive(t *testing.T) {
	s, mockCards, mockBalance, mockTxn := newTestServer(t)

	req := &cardprocessingpb.CardAuthRequest{CardId: "card-frozen"}
	userID := "user-abc"

	// Mock GetCard call to return FROZEN card
	mockCards.On("GetCard", mock.Anything, &cardspb.GetCardRequest{CardId: req.CardId}).
		Return(&cardspb.Card{Id: req.CardId, UserId: userID, Status: "FROZEN"}, nil).Once()

	ctx := context.Background()
	resp, err := s.AuthorizeCardTransaction(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Approved)
	assert.Equal(t, "card is frozen", resp.DeclineReason)

	mockCards.AssertExpectations(t)
	mockBalance.AssertNotCalled(t, "AuthorizeDebit", mock.Anything, mock.Anything)
	mockTxn.AssertNotCalled(t, "RecordTransaction", mock.Anything, mock.Anything)
}

func TestAuthorizeCardTransaction_InsufficientFunds(t *testing.T) {
	s, mockCards, mockBalance, mockTxn := newTestServer(t)

	req := &cardprocessingpb.CardAuthRequest{CardId: "card-123", Amount: 10000}
	userID := "user-abc"

	// Mock GetCard call
	mockCards.On("GetCard", mock.Anything, &cardspb.GetCardRequest{CardId: req.CardId}).
		Return(&cardspb.Card{Id: req.CardId, UserId: userID, Status: "ACTIVE"}, nil).Once()

	// Mock AuthorizeDebit call to return insufficient funds
	mockBalance.On("AuthorizeDebit", mock.Anything, &balancepb.AuthorizeDebitRequest{AccountId: userID, Amount: req.Amount}).
		Return(&balancepb.DebitResult{Success: false, ErrorMessage: "insufficient funds"}, nil).Once()

	ctx := context.Background()
	resp, err := s.AuthorizeCardTransaction(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.Approved)
	assert.Equal(t, "insufficient funds", resp.DeclineReason)

	mockCards.AssertExpectations(t)
	mockBalance.AssertExpectations(t)
	mockTxn.AssertNotCalled(t, "RecordTransaction", mock.Anything, mock.Anything)
}

func TestAuthorizeCardTransaction_RecordTransactionFails(t *testing.T) {
	s, mockCards, mockBalance, mockTxn := newTestServer(t)

	req := &cardprocessingpb.CardAuthRequest{CardId: "card-123", Amount: 1000}
	userID := "user-abc"
	expectedError := errors.New("txn db error")

	// Mock GetCard call
	mockCards.On("GetCard", mock.Anything, &cardspb.GetCardRequest{CardId: req.CardId}).
		Return(&cardspb.Card{Id: req.CardId, UserId: userID, Status: "ACTIVE"}, nil).Once()

	// Mock AuthorizeDebit call
	mockBalance.On("AuthorizeDebit", mock.Anything, &balancepb.AuthorizeDebitRequest{AccountId: userID, Amount: req.Amount}).
		Return(&balancepb.DebitResult{Success: true, NewBalance: 9000}, nil).Once()

	// Mock RecordTransaction call to fail
	mockTxn.On("RecordTransaction", mock.Anything, mock.AnythingOfType("*transactions.TransactionInput")).
		Return(nil, expectedError).Once()

	ctx := context.Background()
	resp, err := s.AuthorizeCardTransaction(ctx, req)

	assert.Error(t, err) // Should return gRPC internal error
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to record transaction after debit")

	mockCards.AssertExpectations(t)
	mockBalance.AssertExpectations(t)
	mockTxn.AssertExpectations(t)
}
