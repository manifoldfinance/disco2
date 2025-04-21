// Package main is the entry point for the API gateway service
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/manifoldfinance/disco2/v2/internal/api/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	balancepb "github.com/manifoldfinance/disco2/v2/pkg/pb/balance"
	cardspb "github.com/manifoldfinance/disco2/v2/pkg/pb/cards"
	discopb "github.com/manifoldfinance/disco2/v2/pkg/pb/disco"
	feedpb "github.com/manifoldfinance/disco2/v2/pkg/pb/feed"
	merchantpb "github.com/manifoldfinance/disco2/v2/pkg/pb/merchant"
	transactionspb "github.com/manifoldfinance/disco2/v2/pkg/pb/transactions"
)

// apiServer holds gRPC client connections to all the services
type apiServer struct {
	balanceClient      balancepb.BalanceClient
	feedClient         feedpb.FeedClient
	transactionsClient transactionspb.TransactionsClient
	merchantClient     merchantpb.MerchantClient
	cardsClient        cardspb.CardsClient
	discoClient        discopb.DiscoPaymentGatewayClient
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Set up gRPC client for Balance service
	balanceConn, err := grpc.Dial(cfg.ServicesURLs["balance"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Balance service: %v", err)
	}
	defer balanceConn.Close()
	balanceClient := balancepb.NewBalanceClient(balanceConn)

	// Set up gRPC client for Feed service
	feedConn, err := grpc.Dial(cfg.ServicesURLs["feed"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Feed service: %v", err)
	}
	defer feedConn.Close()
	feedClient := feedpb.NewFeedClient(feedConn)

	// Set up gRPC client for Transactions service
	transactionsConn, err := grpc.Dial(cfg.ServicesURLs["transactions"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Transactions service: %v", err)
	}
	defer transactionsConn.Close()
	transactionsClient := transactionspb.NewTransactionsClient(transactionsConn)

	// Set up gRPC client for Merchant service
	merchantConn, err := grpc.Dial(cfg.ServicesURLs["merchant"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Merchant service: %v", err)
	}
	defer merchantConn.Close()
	merchantClient := merchantpb.NewMerchantClient(merchantConn)

	// Set up gRPC client for Cards service
	cardsConn, err := grpc.Dial(cfg.ServicesURLs["cards"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Cards service: %v", err)
	}
	defer cardsConn.Close()
	cardsClient := cardspb.NewCardsClient(cardsConn)

	// Set up gRPC client for Disco Payment Gateway service
	discoConn, err := grpc.Dial(cfg.ServicesURLs["disco"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Disco Payment Gateway service: %v", err)
	}
	defer discoConn.Close()
	discoClient := discopb.NewDiscoPaymentGatewayClient(discoConn)

	s := &apiServer{
		balanceClient:      balanceClient,
		feedClient:         feedClient,
		transactionsClient: transactionsClient,
		merchantClient:     merchantClient,
		cardsClient:        cardsClient,
		discoClient:        discoClient,
	}

	// Set up Echo HTTP server
	e := echo.New()
	// Add HTTP routes here
	e.GET("/account/balance/:account_id", s.getBalanceHandler)
	e.GET("/feed/:account_id", s.getFeedHandler)
	e.POST("/cards/:id/freeze", s.freezeCardHandler)

	// Add Disco Payment Gateway routes
	discoGroup := e.Group("/payments/disco")
	discoGroup.POST("/session", s.createDiscoSessionHandler)
	discoGroup.GET("/session/:session_id", s.getDiscoSessionByIdHandler)
	discoGroup.GET("/sessions", s.listDiscoSessionsHandler)
	discoGroup.POST("/estimate", s.estimateDiscoPaymentAmountHandler)
	discoGroup.POST("/wallet", s.createDiscoWalletHandler)

	// Start HTTP server in a goroutine
	httpServer := &http.Server{
		Addr:    cfg.HTTPPort,
		Handler: e,
	}
	go func() {
		log.Println("HTTP server starting on :8080")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start http server: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shut down the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Create a deadline to wait for current operations to complete
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}
	log.Println("Server successfully shut down.")
}

// Implement HTTP handlers here

func (s *apiServer) getBalanceHandler(c echo.Context) error {
	accountID := c.Param("account_id")
	if accountID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "account_id path parameter is required"})
	}

	req := &balancepb.AccountID{AccountId: accountID}

	balanceResp, err := s.balanceClient.GetBalance(c.Request().Context(), req)
	if err != nil {
		// Handle gRPC errors
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
			case codes.NotFound:
				return c.JSON(http.StatusNotFound, map[string]string{"error": st.Message()})
			case codes.Internal:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			default:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "unknown gRPC error"})
			}
		}
		log.Printf("unexpected gRPC error from balance service: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	return c.JSON(http.StatusOK, balanceResp)
}

func (s *apiServer) getFeedHandler(c echo.Context) error {
	accountID := c.Param("account_id")
	if accountID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "account_id path parameter is required"})
	}

	limitStr := c.QueryParam("limit")
	limit := uint32(0)
	if limitStr != "" {
		parsedLimit, err := strconv.ParseUint(limitStr, 10, 32)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid limit parameter"})
		}
		limit = uint32(parsedLimit)
	}

	beforeID := c.QueryParam("before_id")

	// 1. Get feed items from Feed service
	listFeedReq := &feedpb.ListFeedItemsRequest{
		AccountId: accountID,
		Limit:     limit,
		BeforeId:  beforeID,
	}
	feedItemsResp, err := s.feedClient.ListFeedItems(c.Request().Context(), listFeedReq)
	if err != nil {
		log.Printf("failed to get feed items for account %s: %v", accountID, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to get feed items"})
	}

	// 2. Enrich feed items with data from Transactions and Merchants services
	// Collect transaction and merchant IDs from feed items
	txnIDs := []string{}
	merchantIDs := []string{}
	feedItemMap := make(map[string]*feedpb.FeedItem) // Map feed item ID to item for easy lookup

	for _, item := range feedItemsResp.GetItems() {
		feedItemMap[item.GetId()] = item
		if item.GetType() == "TRANSACTION" && item.GetRefId() != "" {
			txnIDs = append(txnIDs, item.GetRefId())
		}
		// Note: Merchant ID is not directly in FeedItem, it's in the Transaction.
		// We'll fetch transactions first, then get merchant IDs from them.
	}

	// Fetch transactions for relevant feed items
	transactionsMap := make(map[string]*transactionspb.Transaction)
	if len(txnIDs) > 0 {
		// Need a way to get transactions by a list of IDs. Transactions service has GetTransaction (by single ID)
		// and ListTransactions (by account_id). We might need a new gRPC method like GetTransactionsByIDs
		// or call GetTransaction for each ID. Calling individually is simpler for now but less efficient.
		// Let's call GetTransaction for each ID for simplicity in this example.
		for _, txnID := range txnIDs {
			txnReq := &transactionspb.TransactionQuery{Id: txnID}
			txn, err := s.transactionsClient.GetTransaction(c.Request().Context(), txnReq)
			if err != nil {
				log.Printf("warning: failed to get transaction %s for feed item: %v", txnID, err)
				// Continue processing other items even if one transaction lookup fails
				continue
			}
			transactionsMap[txn.GetId()] = txn
			if txn.GetMerchantId() != "" {
				merchantIDs = append(merchantIDs, txn.GetMerchantId())
			}
		}
	}

	// Fetch merchants for relevant transactions
	merchantsMap := make(map[string]*merchantpb.MerchantData)
	if len(merchantIDs) > 0 {
		// Need a way to get merchants by a list of IDs. Merchant service has GetMerchant (by single ID)
		// We might need a new gRPC method like GetMerchantsByIDs or call GetMerchant for each ID.
		// Calling individually is simpler for now but less efficient.
		for _, merchantID := range merchantIDs {
			merchantReq := &merchantpb.MerchantID{MerchantId: merchantID}
			merchant, err := s.merchantClient.GetMerchant(c.Request().Context(), merchantReq)
			if err != nil {
				log.Printf("warning: failed to get merchant %s for transaction: %v", merchantID, err)
				// Continue processing other items
				continue
			}
			merchantsMap[merchant.GetMerchantId()] = merchant
		}
	}

	// Combine data: Create a new list of enriched feed items for the response
	var enrichedFeedItems []map[string]interface{}
	for _, item := range feedItemsResp.GetItems() {
		enrichedItem := map[string]interface{}{
			"id":         item.GetId(),
			"account_id": item.GetAccountId(),
			"type":       item.GetType(),
			"timestamp":  item.GetTimestamp(),
			"content":    item.GetContent(),
			"ref_id":     item.GetRefId(),
		}

		// If it's a transaction item, add transaction and merchant details
		if item.GetType() == "TRANSACTION" && item.GetRefId() != "" {
			if txn, ok := transactionsMap[item.GetRefId()]; ok {
				enrichedItem["transaction"] = map[string]interface{}{
					"id":           txn.GetId(),
					"amount":       txn.GetAmount(),
					"currency":     txn.GetCurrency(),
					"status":       txn.GetStatus(),
					"merchant_raw": txn.GetMerchantRaw(),
					"category":     txn.GetCategory(),
					// Add other transaction fields as needed
				}
				if txn.GetMerchantId() != "" {
					if merchant, ok := merchantsMap[txn.GetMerchantId()]; ok {
						enrichedItem["merchant"] = map[string]interface{}{
							"id":       merchant.GetMerchantId(),
							"name":     merchant.GetName(),
							"category": merchant.GetCategory(),
							"logo_url": merchant.GetLogoUrl(),
							// Add other merchant fields as needed
						}
					}
				}
			}
		}
		// Add other item types and their specific data here

		enrichedFeedItems = append(enrichedFeedItems, enrichedItem)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{"items": enrichedFeedItems})
}

func (s *apiServer) freezeCardHandler(c echo.Context) error {
	cardID := c.Param("id")
	if cardID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "card ID path parameter is required"})
	}

	// Assume the request body is empty or contains minimal info, the action is implied by the endpoint
	// In a real API, you might validate user ownership of the card here using auth context

	req := &cardspb.UpdateCardStatusRequest{
		CardId:    cardID,
		NewStatus: "FROZEN", // Hardcode status to FROZEN
	}

	card, err := s.cardsClient.UpdateCardStatus(c.Request().Context(), req)
	if err != nil {
		// Handle gRPC errors
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
			case codes.NotFound:
				return c.JSON(http.StatusNotFound, map[string]string{"error": st.Message()})
			case codes.InvalidArgument:
				return c.JSON(http.StatusBadRequest, map[string]string{"error": st.Message()})
			case codes.Internal:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			default:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "unknown gRPC error"})
			}
		}
		log.Printf("unexpected gRPC error from cards service: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	// Return the updated card details
	return c.JSON(http.StatusOK, card)
}

// --- Disco Payment Gateway Handlers ---

func (s *apiServer) createDiscoSessionHandler(c echo.Context) error {
	req := new(discopb.CreateSessionRequest)
	if err := c.Bind(req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// TODO: Extract user_id from auth context instead of request body if applicable
	// req.UserId = ...

	resp, err := s.discoClient.CreateSession(c.Request().Context(), req)
	if err != nil {
		log.Printf("failed to call CreateSession on disco gateway: %v", err)
		// Map gRPC error to HTTP status
		st, _ := status.FromError(err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to create disco session: %s", st.Message())})
	}
	return c.JSON(http.StatusCreated, resp)
}

func (s *apiServer) getDiscoSessionByIdHandler(c echo.Context) error {
	sessionID := c.Param("session_id")
	if sessionID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "session_id path parameter is required"})
	}

	req := &discopb.GetSessionByIdRequest{SessionId: sessionID}
	resp, err := s.discoClient.GetSessionById(c.Request().Context(), req)
	if err != nil {
		log.Printf("failed to call GetSessionById on disco gateway: %v", err)
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.NotFound {
			return c.JSON(http.StatusNotFound, map[string]string{"error": st.Message()})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to get disco session: %s", st.Message())})
	}
	return c.JSON(http.StatusOK, resp)
}

func (s *apiServer) listDiscoSessionsHandler(c echo.Context) error {
	// Extract query params (user_id, status, limit, cursor)
	userID := c.QueryParam("user_id") // TODO: Get user_id from auth context?
	statusFilter := c.QueryParam("status")
	cursor := c.QueryParam("cursor")
	limitStr := c.QueryParam("limit")
	limit := int32(0)
	if limitStr != "" {
		parsedLimit, err := strconv.ParseInt(limitStr, 10, 32)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid limit parameter"})
		}
		limit = int32(parsedLimit)
	}

	req := &discopb.ListSessionsRequest{
		UserId: userID,
		Status: statusFilter,
		Limit:  limit,
		Cursor: cursor,
	}

	resp, err := s.discoClient.ListSessions(c.Request().Context(), req)
	if err != nil {
		log.Printf("failed to call ListSessions on disco gateway: %v", err)
		st, _ := status.FromError(err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to list disco sessions: %s", st.Message())})
	}
	return c.JSON(http.StatusOK, resp)
}

func (s *apiServer) estimateDiscoPaymentAmountHandler(c echo.Context) error {
	req := new(discopb.EstimatePaymentAmountRequest)
	if err := c.Bind(req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	resp, err := s.discoClient.EstimatePaymentAmount(c.Request().Context(), req)
	if err != nil {
		log.Printf("failed to call EstimatePaymentAmount on disco gateway: %v", err)
		st, _ := status.FromError(err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to estimate disco payment: %s", st.Message())})
	}
	return c.JSON(http.StatusOK, resp)
}

func (s *apiServer) createDiscoWalletHandler(c echo.Context) error {
	req := new(discopb.CreateWalletRequest)
	if err := c.Bind(req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	// TODO: Extract user_id from auth context

	resp, err := s.discoClient.CreateWallet(c.Request().Context(), req)
	if err != nil {
		log.Printf("failed to call CreateWallet on disco gateway: %v", err)
		st, _ := status.FromError(err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to create disco wallet: %s", st.Message())})
	}
	return c.JSON(http.StatusCreated, resp)
}
