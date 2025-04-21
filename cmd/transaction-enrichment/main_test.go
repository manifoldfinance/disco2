package main

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"

	merchantpb "github.com/manifoldfinance/disco2/v2/merchant/merchant"
	transactionspb "github.com/manifoldfinance/disco2/v2/transactions/transactions"
)

// Mock TransactionsClient
type mockTransactionsClient struct {
	mock.Mock
}

func (m *mockTransactionsClient) RecordTransaction(ctx context.Context, in *transactionspb.TransactionInput, opts ...grpc.CallOption) (*transactionspb.Transaction, error) {
	args := m.Called(ctx, in)
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

// Mock MerchantClient
type mockMerchantClient struct {
	mock.Mock
}

func (m *mockMerchantClient) GetMerchant(ctx context.Context, in *merchantpb.MerchantID, opts ...grpc.CallOption) (*merchantpb.MerchantData, error) {
	args := m.Called(ctx, in)
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
	return args.Get(0).(*merchantpb.MerchantData), args.Error(1)
}

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, *mockTransactionsClient, *mockMerchantClient) {
	mockTxnClient := new(mockTransactionsClient)
	mockMerchantClient := new(mockMerchantClient)

	// Create a real Redis client for the test server
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	s := &server{
		redisClient:        rdb,
		transactionsClient: mockTxnClient,
		merchantClient:     mockMerchantClient,
	}
	return s, mockTxnClient, mockMerchantClient
}

func TestEnrichTransaction_Success(t *testing.T) {
	s, mockTxnClient, mockMerchantClient := newTestServer(t)

	transactionID := "txn-123"
	rawMerchantName := "RAW MERCHANT NAME"
	expectedMerchantID := "merch-abc"
	expectedMerchantName := "Clean Merchant Name"
	expectedCategory := "Shopping"

	// Mock GetTransaction call
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(&transactionspb.Transaction{
			Id:          transactionID,
			AccountId:   "acc-1",
			MerchantRaw: rawMerchantName,
			// MerchantId is empty initially
		}, nil).Once()

	// Mock FindOrCreateMerchant call
	mockMerchantClient.On("FindOrCreateMerchant", mock.Anything, &merchantpb.MerchantQuery{RawName: rawMerchantName}).
		Return(&merchantpb.MerchantData{
			MerchantId: expectedMerchantID,
			Name:       expectedMerchantName,
			Category:   expectedCategory,
		}, nil).Once()

	// Mock UpdateTransaction call
	expectedUpdateReq := &transactionspb.UpdateTransactionRequest{
		Id:           transactionID,
		MerchantId:   expectedMerchantID,
		MerchantName: expectedMerchantName,
		Category:     expectedCategory,
	}
	mockTxnClient.On("UpdateTransaction", mock.Anything, expectedUpdateReq).
		Return(&transactionspb.Transaction{ // Return value doesn't matter much here
			Id: transactionID, MerchantId: expectedMerchantID, MerchantName: expectedMerchantName, Category: expectedCategory,
		}, nil).Once()

	ctx := context.Background()
	err := s.enrichTransaction(ctx, transactionID)

	assert.NoError(t, err)
	mockTxnClient.AssertExpectations(t)
	mockMerchantClient.AssertExpectations(t)
}

func TestEnrichTransaction_AlreadyEnriched(t *testing.T) {
	s, mockTxnClient, mockMerchantClient := newTestServer(t)

	transactionID := "txn-456"

	// Mock GetTransaction call - return transaction that already has merchant_id
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(&transactionspb.Transaction{
			Id:          transactionID,
			AccountId:   "acc-2",
			MerchantId:  "merch-existing", // Already has merchant ID
			MerchantRaw: "Some Raw Name",
		}, nil).Once()

	ctx := context.Background()
	err := s.enrichTransaction(ctx, transactionID)

	assert.NoError(t, err)
	mockTxnClient.AssertExpectations(t)
	// Ensure FindOrCreateMerchant and UpdateTransaction were NOT called
	mockMerchantClient.AssertNotCalled(t, "FindOrCreateMerchant", mock.Anything, mock.Anything)
	mockTxnClient.AssertNotCalled(t, "UpdateTransaction", mock.Anything, mock.Anything)
}

func TestEnrichTransaction_GetTransactionFails(t *testing.T) {
	s, mockTxnClient, mockMerchantClient := newTestServer(t)

	transactionID := "txn-789"
	expectedError := errors.New("db error")

	// Mock GetTransaction call to fail
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(nil, expectedError).Once()

	ctx := context.Background()
	err := s.enrichTransaction(ctx, transactionID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get transaction")
	mockTxnClient.AssertExpectations(t)
	mockMerchantClient.AssertNotCalled(t, "FindOrCreateMerchant", mock.Anything, mock.Anything)
	mockTxnClient.AssertNotCalled(t, "UpdateTransaction", mock.Anything, mock.Anything)
}

func TestEnrichTransaction_FindOrCreateMerchantFails(t *testing.T) {
	s, mockTxnClient, mockMerchantClient := newTestServer(t)

	transactionID := "txn-101"
	rawMerchantName := "FAILING MERCHANT"
	expectedError := errors.New("merchant service error")

	// Mock GetTransaction call
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(&transactionspb.Transaction{
			Id:          transactionID,
			AccountId:   "acc-3",
			MerchantRaw: rawMerchantName,
		}, nil).Once()

	// Mock FindOrCreateMerchant call to fail
	mockMerchantClient.On("FindOrCreateMerchant", mock.Anything, &merchantpb.MerchantQuery{RawName: rawMerchantName}).
		Return(nil, expectedError).Once()

	ctx := context.Background()
	err := s.enrichTransaction(ctx, transactionID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find or create merchant")
	mockTxnClient.AssertExpectations(t)
	mockMerchantClient.AssertExpectations(t)
	mockTxnClient.AssertNotCalled(t, "UpdateTransaction", mock.Anything, mock.Anything)
}

func TestEnrichTransaction_UpdateTransactionFails(t *testing.T) {
	s, mockTxnClient, mockMerchantClient := newTestServer(t)

	transactionID := "txn-112"
	rawMerchantName := "UPDATE FAILS MERCHANT"
	expectedMerchantID := "merch-def"
	expectedMerchantName := "Update Fails Merchant"
	expectedCategory := "Errors"
	expectedError := errors.New("update error")

	// Mock GetTransaction call
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(&transactionspb.Transaction{
			Id:          transactionID,
			AccountId:   "acc-4",
			MerchantRaw: rawMerchantName,
		}, nil).Once()

	// Mock FindOrCreateMerchant call
	mockMerchantClient.On("FindOrCreateMerchant", mock.Anything, &merchantpb.MerchantQuery{RawName: rawMerchantName}).
		Return(&merchantpb.MerchantData{
			MerchantId: expectedMerchantID,
			Name:       expectedMerchantName,
			Category:   expectedCategory,
		}, nil).Once()

	// Mock UpdateTransaction call to fail
	expectedUpdateReq := &transactionspb.UpdateTransactionRequest{
		Id:           transactionID,
		MerchantId:   expectedMerchantID,
		MerchantName: expectedMerchantName,
		Category:     expectedCategory,
	}
	mockTxnClient.On("UpdateTransaction", mock.Anything, expectedUpdateReq).
		Return(nil, expectedError).Once()

	ctx := context.Background()
	err := s.enrichTransaction(ctx, transactionID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update transaction")
	mockTxnClient.AssertExpectations(t)
	mockMerchantClient.AssertExpectations(t)
}
