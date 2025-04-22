package main

import (
	"context" // Import context
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http" // Import http
	"os"       // Import the os package
	"strings"  // Import strings
	"time"     // Import time

	"github.com/go-redis/redis/v8" // Import redis
	"github.com/google/uuid"       // Import uuid
	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"  // Import codes
	"google.golang.org/grpc/status" // Import status

	// Import generated protobuf code
	merchantpb "github.com/sambacha/monzo/v2/merchant/merchant"
)

type server struct {
	merchantpb.UnimplementedMerchantServer
	db          *sql.DB
	redisClient *redis.Client
}

func main() {
	// Database connection setup (placeholder)
	db, err := sql.Open("postgres", "user=user dbname=merchant sslmode=disable")
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	// Redis client setup
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379", // Redis address (placeholder)
		DB:   0,                // use default DB
	})

	// Ping Redis to check connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")

	// Auto-migrate schema (for development/testing)
	// In production, use proper schema migration tools
	schemaSQL, err := os.ReadFile("merchant/schema.sql")
	if err != nil {
		log.Fatalf("failed to read schema file: %v", err)
	}
	if _, err := db.Exec(string(schemaSQL)); err != nil {
		log.Fatalf("failed to execute schema: %v", err)
	}
	log.Println("Database schema applied successfully")

	s := &server{db: db, redisClient: rdb}

	// Set up Echo HTTP server
	e := echo.New()
	// Add HTTP routes here
	e.GET("/merchants/:id", s.getMerchantHandler)
	e.PUT("/merchants/:id", s.updateMerchantHandler)

	// Set up gRPC server (placeholder)
	grpcServer := grpc.NewServer()
	merchantpb.RegisterMerchantServer(grpcServer, s)

	// Start HTTP server (placeholder)
	go func() {
		if err := e.Start(":8083"); err != nil && err != http.ErrServerClosed { // Use a different port
			log.Fatalf("failed to start http server: %v", err)
		}
	}()

	// Start gRPC server (placeholder)
	lis, err := net.Listen("tcp", ":50054") // Use a different port
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	log.Printf("gRPC server listening on %v", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Implement gRPC methods here

func (s *server) GetMerchant(ctx context.Context, req *merchantpb.MerchantID) (*merchantpb.MerchantData, error) {
	log.Printf("Received GetMerchant request: %+v", req)

	query := `SELECT merchant_id, name, category, logo_url, mcc FROM merchants WHERE merchant_id = $1`

	var merchant merchantpb.MerchantData
	var category sql.NullString
	var logoURL sql.NullString
	var mcc sql.NullInt32

	err := s.db.QueryRowContext(ctx, query, req.GetMerchantId()).Scan(
		&merchant.MerchantId,
		&merchant.Name,
		&category,
		&logoURL,
		&mcc,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("merchant not found: %s", req.GetMerchantId())
			return nil, status.Errorf(codes.NotFound, "merchant not found")
		}
		log.Printf("failed to get merchant: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get merchant")
	}

	merchant.Category = category.String
	merchant.LogoUrl = logoURL.String
	merchant.Mcc = mcc.Int32

	return &merchant, nil
}

func (s *server) FindOrCreateMerchant(ctx context.Context, req *merchantpb.MerchantQuery) (*merchantpb.MerchantData, error) {
	log.Printf("Received FindOrCreateMerchant request: %+v", req)

	// Simple lookup by lowercased raw name and MCC
	lookupQuery := `SELECT merchant_id, name, category, logo_url, mcc FROM merchants WHERE lower(name) = lower($1) AND coalesce(mcc, 0) = coalesce($2, 0)`

	var merchant merchantpb.MerchantData
	var category sql.NullString
	var logoURL sql.NullString
	var mcc sql.NullInt32

	err := s.db.QueryRowContext(ctx, lookupQuery, req.GetRawName(), sql.NullInt32{Int32: req.GetMcc(), Valid: req.Mcc != 0}).Scan(
		&merchant.MerchantId,
		&merchant.Name,
		&category,
		&logoURL,
		&mcc,
	)

	if err == nil {
		// Merchant found, return it
		merchant.Category = category.String
		merchant.LogoUrl = logoURL.String
		merchant.Mcc = mcc.Int32
		log.Printf("Found existing merchant: %s", merchant.GetMerchantId())
		return &merchant, nil
	}

	if err != sql.ErrNoRows {
		// An error occurred other than not found
		log.Printf("failed to lookup merchant: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to find or create merchant")
	}

	// Merchant not found, create a new one
	merchantID := uuid.New().String()
	cleanedName := strings.Title(strings.ToLower(req.GetRawName())) // Simple cleaning
	defaultCategory := ""                                           // TODO: Map MCC to category

	insertQuery := `INSERT INTO merchants (merchant_id, name, category, mcc, created_at, updated_at)
					VALUES ($1, $2, $3, $4, NOW(), NOW())
					RETURNING merchant_id, name, category, logo_url, mcc`

	err = s.db.QueryRowContext(ctx, insertQuery,
		merchantID,
		cleanedName,
		sql.NullString{String: defaultCategory, Valid: defaultCategory != ""},
		sql.NullInt32{Int32: req.GetMcc(), Valid: req.Mcc != 0},
	).Scan(
		&merchant.MerchantId,
		&merchant.Name,
		&category,
		&logoURL,
		&mcc,
	)
	if err != nil {
		// Handle potential unique constraint violation if another request created it simultaneously
		if strings.Contains(err.Error(), "unique constraint") {
			log.Printf("race condition: merchant already created, retrying lookup")
			// Retry the lookup to get the existing merchant
			return s.FindOrCreateMerchant(ctx, req)
		}
		log.Printf("failed to insert new merchant: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to create merchant")
	}

	merchant.Category = category.String
	merchant.LogoUrl = logoURL.String
	merchant.Mcc = mcc.Int32

	log.Printf("Created new merchant: %s", merchant.GetMerchantId())

	// TODO: Publish "merchant.created" event? (Spec only mentions merchant.updated)

	return &merchant, nil
}

func (s *server) UpdateMerchant(ctx context.Context, req *merchantpb.UpdateMerchantRequest) (*merchantpb.MerchantData, error) {
	log.Printf("Received UpdateMerchant request: %+v", req)

	// Build the update query dynamically based on provided fields
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.GetName() != "" {
		updates = append(updates, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, req.GetName())
		argIndex++
	}
	if req.GetCategory() != "" {
		updates = append(updates, fmt.Sprintf("category = $%d", argIndex))
		args = append(args, req.GetCategory())
		argIndex++
	}
	if req.GetLogoUrl() != "" {
		updates = append(updates, fmt.Sprintf("logo_url = $%d", argIndex))
		args = append(args, req.GetLogoUrl())
		argIndex++
	}
	if req.GetMcc() != 0 {
		updates = append(updates, fmt.Sprintf("mcc = $%d", argIndex))
		args = append(args, req.GetMcc())
		argIndex++
	}

	if len(updates) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "no fields to update")
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	query := fmt.Sprintf(`UPDATE merchants SET %s WHERE merchant_id = $%d RETURNING merchant_id, name, category, logo_url, mcc`,
		strings.Join(updates, ", "), argIndex)
	args = append(args, req.GetMerchantId())

	var updatedMerchant merchantpb.MerchantData
	var category sql.NullString
	var logoURL sql.NullString
	var mcc sql.NullInt32

	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&updatedMerchant.MerchantId,
		&updatedMerchant.Name,
		&category,
		&logoURL,
		&mcc,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("merchant not found for update: %s", req.GetMerchantId())
			return nil, status.Errorf(codes.NotFound, "merchant not found")
		}
		log.Printf("failed to update merchant: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to update merchant")
	}

	updatedMerchant.Category = category.String
	updatedMerchant.LogoUrl = logoURL.String
	updatedMerchant.Mcc = mcc.Int32

	log.Printf("Successfully updated merchant: %s", updatedMerchant.GetMerchantId())

	// Publish "merchant.updated" event to Redis
	eventPayload := fmt.Sprintf(`{"merchant_id": "%s", "name": "%s", "category": "%s", "logo_url": "%s", "mcc": %d}`,
		updatedMerchant.GetMerchantId(),
		updatedMerchant.GetName(),
		updatedMerchant.GetCategory(),
		updatedMerchant.GetLogoUrl(),
		updatedMerchant.GetMcc(),
	)
	if _, err := s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: "merchant:updated",
		MaxLen: 0, // No limit
		Values: map[string]interface{}{
			"payload": eventPayload,
		},
	}).Result(); err != nil {
		log.Printf("failed to publish merchant:updated event: %v", err)
		// Log the error
	} else {
		log.Printf("Published merchant:updated event for merchant %s", updatedMerchant.GetMerchantId())
	}

	return &updatedMerchant, nil
}

// Implement HTTP handlers here

func (s *server) getMerchantHandler(c echo.Context) error {
	merchantID := c.Param("id")
	req := &merchantpb.MerchantID{MerchantId: merchantID}

	merchant, err := s.GetMerchant(c.Request().Context(), req)
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
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	return c.JSON(http.StatusOK, merchant)
}

func (s *server) updateMerchantHandler(c echo.Context) error {
	merchantID := c.Param("id")

	var updateReq struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		LogoUrl  string `json:"logo_url"`
		Mcc      int32  `json:"mcc"`
	}
	if err := c.Bind(&updateReq); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	req := &merchantpb.UpdateMerchantRequest{
		MerchantId: merchantID,
		Name:       updateReq.Name,
		Category:   updateReq.Category,
		LogoUrl:    updateReq.LogoUrl,
		Mcc:        updateReq.Mcc,
	}

	merchant, err := s.UpdateMerchant(c.Request().Context(), req)
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
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	return c.JSON(http.StatusOK, merchant)
}
