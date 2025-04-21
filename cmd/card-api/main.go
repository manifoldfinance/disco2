package main

import (
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	cardprocessingpb "github.com/manifoldfinance/disco2/v2/card_processing"
)

type server struct {
	cardProcessingClient cardprocessingpb.CardProcessingClient
}

func main() {
	// Set up gRPC client for Card-Processing service
	cardProcessingConn, err := grpc.Dial("localhost:50050", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Card-Processing service: %v", err)
	}
	defer cardProcessingConn.Close()
	cardProcessingClient := cardprocessingpb.NewCardProcessingClient(cardProcessingConn)

	s := &server{
		cardProcessingClient: cardProcessingClient,
	}

	// Set up Echo HTTP server
	e := echo.New()
	e.POST("/cardAuth", s.cardAuthHandler)

	// Start HTTP server
	if err := e.Start(":8086"); err != nil && err != http.ErrServerClosed {
		log.Fatalf("failed to start http server: %v", err)
	}
}

// Implement HTTP handlers here

func (s *server) cardAuthHandler(c echo.Context) error {
	var req struct {
		CardId       string `json:"card_id"`
		Amount       int64  `json:"amount"`
		Currency     string `json:"currency"`
		MerchantId   string `json:"merchant_id"`
		MerchantName string `json:"merchant_name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Create the gRPC request
	grpcReq := &cardprocessingpb.CardAuthRequest{
		CardId:       req.CardId,
		Amount:       req.Amount,
		Currency:     req.Currency,
		MerchantId:   req.MerchantId,
		MerchantName: req.MerchantName,
	}

	// Call the Card-Processing service
	grpcResp, err := s.cardProcessingClient.AuthorizeCardTransaction(c.Request().Context(), grpcReq)
	if err != nil {
		// Handle gRPC errors and map to HTTP status codes
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
			case codes.InvalidArgument:
				return c.JSON(http.StatusBadRequest, map[string]string{"error": st.Message()})
			case codes.NotFound: // Card or Account not found errors from downstream
				return c.JSON(http.StatusBadRequest, map[string]string{"error": st.Message()}) // Card network might expect 400 for business errors
			case codes.Internal:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			default:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "unknown gRPC error"})
			}
		}
		log.Printf("unexpected gRPC error from card-processing: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	// Return the result based on the gRPC response
	return c.JSON(http.StatusOK, map[string]interface{}{
		"approved": grpcResp.GetApproved(),
		"reason":   grpcResp.GetDeclineReason(),
	})
}
