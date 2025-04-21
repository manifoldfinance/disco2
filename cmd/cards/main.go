package main

import (
	"context" // Import context
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http" // Import http
	"os"       // Import the os package

	"github.com/go-redis/redis/v8" // Import redis
	"github.com/google/uuid"       // Import uuid
	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"  // Import codes
	"google.golang.org/grpc/status" // Import status

	// Import generated protobuf code
	cardspb "github.com/manifoldfinance/disco2/v2/cards/cards"
)

type server struct {
	cardspb.UnimplementedCardsServer
	db          *sql.DB
	redisClient *redis.Client // Add Redis client
}

func main() {
	// Database connection setup (placeholder)
	db, err := sql.Open("postgres", "user=user dbname=cards sslmode=disable")
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
	schemaSQL, err := os.ReadFile("cards/schema.sql")
	if err != nil {
		log.Fatalf("failed to read schema file: %v", err)
	}
	if _, err := db.Exec(string(schemaSQL)); err != nil {
		log.Fatalf("failed to execute schema: %v", err)
	}
	log.Println("Database schema applied successfully")

	s := &server{db: db}

	// Set up Echo HTTP server
	e := echo.New()
	// Add HTTP routes here
	e.POST("/cards", s.createCardHandler)
	e.GET("/cards/:id", s.getCardHandler)
	e.PATCH("/cards/:id/status", s.updateCardStatusHandler)

	// Set up gRPC server (placeholder)
	grpcServer := grpc.NewServer()
	cardspb.RegisterCardsServer(grpcServer, s)

	// Start HTTP server (placeholder)
	go func() {
		if err := e.Start(":8080"); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start http server: %v", err)
		}
	}()

	// Start gRPC server (placeholder)
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	log.Printf("gRPC server listening on %v", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Implement gRPC methods here

func (s *server) CreateCard(ctx context.Context, req *cardspb.CreateCardRequest) (*cardspb.Card, error) {
	log.Printf("Received CreateCard request: %+v", req)

	cardID := uuid.New().String()
	status := "ACTIVE" // Default status

	query := `INSERT INTO cards (card_id, user_id, status, created_at) VALUES ($1, $2, $3, NOW()) RETURNING card_id, user_id, status, last_four, created_at, updated_at`

	var createdCard cardspb.Card
	err := s.db.QueryRowContext(ctx, query, cardID, req.GetUserId(), status).Scan(
		&createdCard.Id,
		&createdCard.UserId,
		&createdCard.Status,
		&createdCard.LastFour, // last_four will be null initially
		// created_at and updated_at are not in the proto message, skip scanning them
	)
	if err != nil {
		log.Printf("failed to insert card: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to create card")
	}

	// TODO: Publish "card.created" event to Redis
	// Event payload could be JSON or protobuf binary
	eventPayload := fmt.Sprintf(`{"card_id": "%s", "user_id": "%s", "status": "%s"}`, createdCard.GetId(), createdCard.GetUserId(), createdCard.GetStatus())
	if _, err := s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: "card:created",
		MaxLen: 0, // No limit
		Values: map[string]interface{}{
			"payload": eventPayload,
		},
	}).Result(); err != nil {
		log.Printf("failed to publish card:created event: %v", err)
		// Depending on requirements, failure to publish event might be critical or just logged
		// For now, just log the error
	} else {
		log.Printf("Published card:created event for card %s", createdCard.GetId())
	}

	return &createdCard, nil
}

func (s *server) GetCard(ctx context.Context, req *cardspb.GetCardRequest) (*cardspb.Card, error) {
	log.Printf("Received GetCard request: %+v", req)

	query := `SELECT card_id, user_id, status, last_four FROM cards WHERE card_id = $1`

	var card cardspb.Card
	err := s.db.QueryRowContext(ctx, query, req.GetCardId()).Scan(
		&card.Id,
		&card.UserId,
		&card.Status,
		&card.LastFour,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("card not found: %s", req.GetCardId())
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		log.Printf("failed to get card: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get card")
	}

	return &card, nil
}

func (s *server) UpdateCardStatus(ctx context.Context, req *cardspb.UpdateCardStatusRequest) (*cardspb.Card, error) {
	log.Printf("Received UpdateCardStatus request: %+v", req)

	// Basic validation for status (database check constraint is also there)
	validStatuses := map[string]bool{"ACTIVE": true, "INACTIVE": true, "FROZEN": true, "CLOSED": true}
	if !validStatuses[req.GetNewStatus()] {
		return nil, status.Errorf(codes.InvalidArgument, "invalid card status: %s", req.GetNewStatus())
	}

	query := `UPDATE cards SET status = $1, updated_at = NOW() WHERE card_id = $2 RETURNING card_id, user_id, status, last_four`

	var updatedCard cardspb.Card
	err := s.db.QueryRowContext(ctx, query, req.GetNewStatus(), req.GetCardId()).Scan(
		&updatedCard.Id,
		&updatedCard.UserId,
		&updatedCard.Status,
		&updatedCard.LastFour,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("card not found for update: %s", req.GetCardId())
			return nil, status.Errorf(codes.NotFound, "card not found")
		}
		log.Printf("failed to update card status: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to update card status")
	}

	// TODO: Publish "card.status_changed" event to Redis
	eventPayload := fmt.Sprintf(`{"card_id": "%s", "user_id": "%s", "new_status": "%s"}`, updatedCard.GetId(), updatedCard.GetUserId(), updatedCard.GetStatus())
	if _, err := s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: "card:status_changed",
		MaxLen: 0, // No limit
		Values: map[string]interface{}{
			"payload": eventPayload,
		},
	}).Result(); err != nil {
		log.Printf("failed to publish card:status_changed event: %v", err)
		// Log the error
	} else {
		log.Printf("Published card:status_changed event for card %s", updatedCard.GetId())
	}

	return &updatedCard, nil
}

// Implement HTTP handlers here

func (s *server) createCardHandler(c echo.Context) error {
	req := new(cardspb.CreateCardRequest)
	if err := c.Bind(req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	card, err := s.CreateCard(c.Request().Context(), req)
	if err != nil {
		// Handle gRPC errors and map to HTTP status codes
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
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

	return c.JSON(http.StatusCreated, card)
}

func (s *server) getCardHandler(c echo.Context) error {
	cardID := c.Param("id")
	req := &cardspb.GetCardRequest{CardId: cardID}

	card, err := s.GetCard(c.Request().Context(), req)
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

	return c.JSON(http.StatusOK, card)
}

func (s *server) updateCardStatusHandler(c echo.Context) error {
	cardID := c.Param("id")

	var updateReq struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&updateReq); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	req := &cardspb.UpdateCardStatusRequest{
		CardId:    cardID,
		NewStatus: updateReq.Status,
	}

	card, err := s.UpdateCardStatus(c.Request().Context(), req)
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

	return c.JSON(http.StatusOK, card)
}
