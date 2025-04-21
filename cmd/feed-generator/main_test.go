package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"

	feedpb "github.com/manifoldfinance/disco2/v2/feed/feed"
	transactionspb "github.com/manifoldfinance/disco2/v2/transactions/transactions"
)

// Mock TransactionsClient (copied from transaction-enrichment test)
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

// Mock FeedClient
type mockFeedClient struct {
	mock.Mock
}

func (m *mockFeedClient) AddFeedItem(ctx context.Context, in *feedpb.AddFeedItemRequest, opts ...grpc.CallOption) (*feedpb.FeedItem, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*feedpb.FeedItem), args.Error(1)
}

func (m *mockFeedClient) ListFeedItems(ctx context.Context, in *feedpb.ListFeedItemsRequest, opts ...grpc.CallOption) (*feedpb.FeedItems, error) {
	args := m.Called(ctx, in)
	return args.Get(0).(*feedpb.FeedItems), args.Error(1)
}

func (m *mockFeedClient) GetFeedItemsByID(ctx context.Context, in *feedpb.FeedItemIDs, opts ...grpc.CallOption) (*feedpb.FeedItems, error) {
	args := m.Called(ctx, in)
	return args.Get(0).(*feedpb.FeedItems), args.Error(1)
}

// Helper function to create a server instance with mocks
func newTestServer(t *testing.T) (*server, *mockTransactionsClient, *mockFeedClient) {
	mockTxnClient := new(mockTransactionsClient)
	mockFeedClient := new(mockFeedClient)

	// Create a real Redis client for the test server
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	s := &server{
		redisClient:        rdb,
		transactionsClient: mockTxnClient,
		feedClient:         mockFeedClient,
	}
	return s, mockTxnClient, mockFeedClient
}

func TestGenerateFeedItemForTransaction_Success(t *testing.T) {
	s, mockTxnClient, mockFeedClient := newTestServer(t)

	transactionID := "txn-123"
	accountID := "acc-1"
	merchantName := "Test Merchant"
	amount := int64(1000) // 10.00
	currency := "GBP"
	timestamp := time.Now().Format(time.RFC3339)
	expectedFeedItemID := "feed-abc"
	expectedContent := fmt.Sprintf("Spent %.2f %s at %s", float64(amount)/100.0, currency, merchantName)

	// Mock GetTransaction call
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(&transactionspb.Transaction{
			Id:           transactionID,
			AccountId:    accountID,
			Amount:       amount,
			Currency:     currency,
			MerchantName: merchantName, // Assume already enriched for simplicity here
			Timestamp:    timestamp,
		}, nil).Once()

	// Mock AddFeedItem call
	expectedAddFeedReq := &feedpb.AddFeedItemRequest{
		AccountId: accountID,
		Type:      "TRANSACTION",
		Content:   expectedContent,
		RefId:     transactionID,
		Timestamp: timestamp,
	}
	mockFeedClient.On("AddFeedItem", mock.Anything, expectedAddFeedReq).
		Return(&feedpb.FeedItem{
			Id:        expectedFeedItemID,
			AccountId: accountID,
			Type:      "TRANSACTION",
			Content:   expectedContent,
			RefId:     transactionID,
			Timestamp: timestamp,
		}, nil).Once()

	ctx := context.Background()
	err := s.generateFeedItemForTransaction(ctx, transactionID)

	assert.NoError(t, err)
	mockTxnClient.AssertExpectations(t)
	mockFeedClient.AssertExpectations(t)
}

func TestGenerateFeedItemForTransaction_GetTransactionFails(t *testing.T) {
	s, mockTxnClient, mockFeedClient := newTestServer(t)

	transactionID := "txn-fail-get"
	expectedError := errors.New("txn service error")

	// Mock GetTransaction call to fail
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(nil, expectedError).Once()

	ctx := context.Background()
	err := s.generateFeedItemForTransaction(ctx, transactionID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get transaction")
	mockTxnClient.AssertExpectations(t)
	mockFeedClient.AssertNotCalled(t, "AddFeedItem", mock.Anything, mock.Anything)
}

func TestGenerateFeedItemForTransaction_AddFeedItemFails(t *testing.T) {
	s, mockTxnClient, mockFeedClient := newTestServer(t)

	transactionID := "txn-fail-add"
	accountID := "acc-2"
	merchantName := "Test Merchant 2"
	amount := int64(2000)
	currency := "USD"
	timestamp := time.Now().Format(time.RFC3339)
	expectedError := errors.New("feed service error")

	// Mock GetTransaction call
	mockTxnClient.On("GetTransaction", mock.Anything, &transactionspb.TransactionQuery{Id: transactionID}).
		Return(&transactionspb.Transaction{
			Id:           transactionID,
			AccountId:    accountID,
			Amount:       amount,
			Currency:     currency,
			MerchantName: merchantName,
			Timestamp:    timestamp,
		}, nil).Once()

	// Mock AddFeedItem call to fail
	mockFeedClient.On("AddFeedItem", mock.Anything, mock.AnythingOfType("*feed.AddFeedItemRequest")).
		Return(nil, expectedError).Once()

	ctx := context.Background()
	err := s.generateFeedItemForTransaction(ctx, transactionID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add feed item")
	mockTxnClient.AssertExpectations(t)
	mockFeedClient.AssertExpectations(t)
}

// Note: Testing the Redis publish failure is less critical as the feed item is already created.
// We could add a test, but it would look similar to the success case, just asserting the log message.
