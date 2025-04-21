package main

import (
	"bytes"         // Import bytes
	"context"       // Import context
	"encoding/json" // Import json
	"fmt"           // Import fmt
	"io"            // Import io
	"log"
	"net"
	"net/http" // Import http
	"net/url"  // Import url
	"strings"  // Import strings
	"time"     // Import time

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"  // Import codes
	"google.golang.org/grpc/status" // Import status

	// Import generated protobuf code
	discopb "github.com/manifoldfinance/disco2/v2/disco_payment_gateway"
)

type server struct {
	discopb.UnimplementedDiscoPaymentGatewayServer
	httpClient *http.Client
	baseURL    string
	projectID  string
	apiKey     string // Optional API Key
}

func main() {
	// Configuration (placeholders)
	baseURL := "https://api.paywithglide.xyz" // External Disco API base URL
	projectID := "YOUR_PROJECT_ID"            // Your Disco Project ID
	apiKey := "YOUR_API_KEY"                  // Your Disco API Key (optional)

	// Set up HTTP client for external API calls
	httpClient := &http.Client{Timeout: 10 * time.Second} // Add timeout

	s := &server{
		httpClient: httpClient,
		baseURL:    baseURL,
		projectID:  projectID,
		apiKey:     apiKey,
	}

	// Set up gRPC server
	grpcServer := grpc.NewServer()
	discopb.RegisterDiscoPaymentGatewayServer(grpcServer, s)

	// Start gRPC server
	lis, err := net.Listen("tcp", ":50057") // Use a different port (e.g., 50057 for disco-payment-gateway)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	log.Printf("gRPC server listening on %v", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Implement gRPC methods here

func (s *server) CreateSession(ctx context.Context, req *discopb.CreateSessionRequest) (*discopb.CreateSessionResponse, error) {
	log.Printf("Received CreateSession request: %+v", req)

	// Construct request body for external API
	// Note: GLIDE.md mentions snake_case conversion. We should handle this.
	// For simplicity here, assume external API accepts camelCase or we handle conversion.
	// A real implementation would use a library or manual mapping for snake_case.
	externalReqBody := map[string]interface{}{
		"userId":      req.GetUserId(), // Assuming camelCase for now
		"currency":    req.GetCurrency(),
		"amount":      req.GetAmount(),
		"redirectUrl": req.GetRedirectUrl(),
		// Add other necessary fields based on actual Disco API docs
	}

	respBody, err := s.makeDiscoRequest(ctx, http.MethodPost, "/sessions", externalReqBody)
	if err != nil {
		log.Printf("CreateSession request failed: %v", err)
		// Map external API errors to gRPC status codes if possible
		return nil, status.Errorf(codes.Internal, "failed to create session: %v", err)
	}

	// Unmarshal response
	var resp discopb.CreateSessionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Printf("failed to unmarshal CreateSession response: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to parse create session response")
	}

	return &resp, nil
}

func (s *server) GetSessionById(ctx context.Context, req *discopb.GetSessionByIdRequest) (*discopb.GetSessionResponse, error) {
	log.Printf("Received GetSessionById request: %+v", req)

	if req.GetSessionId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	endpoint := fmt.Sprintf("/sessions/%s", req.GetSessionId())

	respBody, err := s.makeDiscoRequest(ctx, http.MethodGet, endpoint, nil) // GET request, no body
	if err != nil {
		log.Printf("GetSessionById request failed: %v", err)
		// Map external API errors to gRPC status codes if possible
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") { // Basic error checking
			return nil, status.Errorf(codes.NotFound, "session not found: %s", req.GetSessionId())
		}
		return nil, status.Errorf(codes.Internal, "failed to get session: %v", err)
	}

	// Unmarshal response
	var resp discopb.GetSessionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Printf("failed to unmarshal GetSessionById response: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to parse get session response")
	}

	return &resp, nil
}

func (s *server) ListSessions(ctx context.Context, req *discopb.ListSessionsRequest) (*discopb.ListSessionsResponse, error) {
	log.Printf("Received ListSessions request: %+v", req)

	endpoint := "/sessions"
	queryParams := url.Values{}

	// Add query parameters, converting to snake_case
	if req.GetUserId() != "" {
		queryParams.Set("user_id", req.GetUserId()) // Assuming external API uses snake_case
	}
	if req.GetStatus() != "" {
		queryParams.Set("status", req.GetStatus())
	}
	if req.GetLimit() > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", req.GetLimit()))
	}
	if req.GetCursor() != "" {
		queryParams.Set("cursor", req.GetCursor())
	}

	if len(queryParams) > 0 {
		endpoint += "?" + queryParams.Encode()
	}

	respBody, err := s.makeDiscoRequest(ctx, http.MethodGet, endpoint, nil) // GET request, no body
	if err != nil {
		log.Printf("ListSessions request failed: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list sessions: %v", err)
	}

	// Unmarshal response
	var resp discopb.ListSessionsResponse
	// Assuming the external API returns a structure like {"sessions": [...], "next_cursor": "..."}
	// We need to map this to our protobuf structure.
	var externalResp struct {
		Sessions   []json.RawMessage `json:"sessions"` // Get raw messages first
		NextCursor string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(respBody, &externalResp); err != nil {
		log.Printf("failed to unmarshal ListSessions external response: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to parse list sessions response")
	}

	resp.NextCursor = externalResp.NextCursor
	resp.Sessions = make([]*discopb.GetSessionResponse, 0, len(externalResp.Sessions))

	for _, rawSession := range externalResp.Sessions {
		var session discopb.GetSessionResponse
		if err := json.Unmarshal(rawSession, &session); err != nil {
			log.Printf("failed to unmarshal individual session in ListSessions: %v", err)
			// Skip this session or return error? Skipping for now.
			continue
		}
		resp.Sessions = append(resp.Sessions, &session)
	}

	return &resp, nil
}

func (s *server) GetSessionByPaymentTransaction(ctx context.Context, req *discopb.GetSessionByPaymentTransactionRequest) (*discopb.GetSessionResponse, error) {
	log.Printf("Received GetSessionByPaymentTransaction request: %+v", req)

	if req.GetPaymentTransactionHash() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "payment_transaction_hash is required")
	}

	endpoint := "/sessions/get-by-payment-transaction"
	queryParams := url.Values{}
	queryParams.Set("payment_transaction_hash", req.GetPaymentTransactionHash()) // Assuming snake_case
	endpoint += "?" + queryParams.Encode()

	respBody, err := s.makeDiscoRequest(ctx, http.MethodGet, endpoint, nil) // GET request, no body
	if err != nil {
		log.Printf("GetSessionByPaymentTransaction request failed: %v", err)
		// Map external API errors to gRPC status codes if possible
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") { // Basic error checking
			return nil, status.Errorf(codes.NotFound, "session not found for payment transaction: %s", req.GetPaymentTransactionHash())
		}
		return nil, status.Errorf(codes.Internal, "failed to get session by payment transaction: %v", err)
	}

	// Unmarshal response
	var resp discopb.GetSessionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Printf("failed to unmarshal GetSessionByPaymentTransaction response: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to parse get session by payment transaction response")
	}

	return &resp, nil
}

func (s *server) CreateWallet(ctx context.Context, req *discopb.CreateWalletRequest) (*discopb.CreateWalletResponse, error) {
	log.Printf("Received CreateWallet request: %+v", req)

	// Construct request body for external API
	externalReqBody := map[string]interface{}{
		"userId": req.GetUserId(), // Assuming camelCase for now
		// Add other necessary fields based on actual Disco API docs
	}

	respBody, err := s.makeDiscoRequest(ctx, http.MethodPost, "/wallets", externalReqBody)
	if err != nil {
		log.Printf("CreateWallet request failed: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to create wallet: %v", err)
	}

	// Unmarshal response
	var resp discopb.CreateWalletResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Printf("failed to unmarshal CreateWallet response: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to parse create wallet response")
	}

	return &resp, nil
}

func (s *server) EstimatePaymentAmount(ctx context.Context, req *discopb.EstimatePaymentAmountRequest) (*discopb.EstimatePaymentAmountResponse, error) {
	log.Printf("Received EstimatePaymentAmount request: %+v", req)

	// Construct request body for external API
	externalReqBody := map[string]interface{}{
		"targetCurrency": req.GetTargetCurrency(), // Assuming camelCase
		"targetAmount":   req.GetTargetAmount(),
		"sourceCurrency": req.GetSourceCurrency(),
		// Add other necessary fields based on actual Disco API docs
	}

	respBody, err := s.makeDiscoRequest(ctx, http.MethodPost, "/estimate-payment-amount", externalReqBody)
	if err != nil {
		log.Printf("EstimatePaymentAmount request failed: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to estimate payment amount: %v", err)
	}

	// Unmarshal response
	var resp discopb.EstimatePaymentAmountResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Printf("failed to unmarshal EstimatePaymentAmount response: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to parse estimate payment amount response")
	}

	return &resp, nil
}

// Helper function to make HTTP requests to the external Disco API
func (s *server) makeDiscoRequest(ctx context.Context, method, endpoint string, body interface{}) ([]byte, error) {
	url := s.baseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Glide-Project-ID", s.projectID) // Use X-Glide-Project-ID as per GLIDE.md
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Attempt to parse error response from external API
		var errorResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if unmarshalErr := json.Unmarshal(respBody, &errorResp); unmarshalErr == nil {
			return nil, fmt.Errorf("external API returned error status %d: %s - %s", resp.StatusCode, errorResp.Error, errorResp.Message)
		}

		return nil, fmt.Errorf("external API returned unexpected status code: %d", resp.StatusCode)
	}

	return respBody, nil
}
