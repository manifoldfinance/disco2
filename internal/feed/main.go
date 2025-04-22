package main

import (
	"context" // Import context
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http" // Import http
	"os"       // Import the os package
	"strconv"  // Import strconv
	"strings"  // Import strings
	"time"     // Import time

	"github.com/go-redis/redis/v8" // Import redis (even if not used for pub/sub, might be used for caching)
	"github.com/google/uuid"       // Import uuid
	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"  // Import codes
	"google.golang.org/grpc/codes"  // Import codes
	"google.golang.org/grpc/status" // Import status

	// Import generated protobuf code
	feedpb "github.com/sambacha/monzo/v2/feed/feed"
)

type server struct {
	feedpb.UnimplementedFeedServer
	db          *sql.DB
	redisClient *redis.Client // Keep Redis client in case needed later
}

func main() {
	// Database connection setup (placeholder)
	db, err := sql.Open("postgres", "user=user dbname=feed sslmode=disable")
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	// Redis client setup (placeholder)
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379", // Redis address (placeholder)
		DB:   0,                // use default DB
	})

	// Ping Redis to check connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("warning: failed to connect to Redis: %v", err) // Log as warning if Redis is optional for this service
	} else {
		log.Println("Connected to Redis")
	}

	// Auto-migrate schema (for development/testing)
	// In production, use proper schema migration tools
	schemaSQL, err := os.ReadFile("feed/schema.sql")
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
	e.GET("/feed", s.listFeedItemsHandler)

	// Set up gRPC server (placeholder)
	grpcServer := grpc.NewServer()
	feedpb.RegisterFeedServer(grpcServer, s)

	// Start HTTP server (placeholder)
	go func() {
		if err := e.Start(":8084"); err != nil && err != http.ErrServerClosed { // Use a different port
			log.Fatalf("failed to start http server: %v", err)
		}
	}()

	// Start gRPC server (placeholder)
	lis, err := net.Listen("tcp", ":50055") // Use a different port
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	log.Printf("gRPC server listening on %v", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Implement gRPC methods here

func (s *server) AddFeedItem(ctx context.Context, req *feedpb.AddFeedItemRequest) (*feedpb.FeedItem, error) {
	log.Printf("Received AddFeedItem request: %+v", req)

	feedItemID := uuid.New().String()

	// Parse the provided timestamp or use current time if not provided/invalid
	itemTimestamp := time.Now()
	if req.GetTimestamp() != "" {
		parsedTime, err := time.Parse(time.RFC3339, req.GetTimestamp())
		if err == nil {
			itemTimestamp = parsedTime
		} else {
			log.Printf("warning: failed to parse timestamp '%s', using current time: %v", req.GetTimestamp(), err)
		}
	}

	query := `INSERT INTO feed_items (id, account_id, type, content, ref_id, timestamp)
			  VALUES ($1, $2, $3, $4, $5, $6)
			  RETURNING id, account_id, type, content, ref_id, timestamp`

	var createdItem feedpb.FeedItem
	var content sql.NullString
	var refID sql.NullString
	var timestamp time.Time

	err := s.db.QueryRowContext(ctx, query,
		feedItemID,
		req.GetAccountId(),
		req.GetType(),
		sql.NullString{String: req.GetContent(), Valid: req.GetContent() != ""},
		sql.NullString{String: req.GetRefId(), Valid: req.GetRefId() != ""},
		itemTimestamp,
	).Scan(
		&createdItem.Id,
		&createdItem.AccountId,
		&createdItem.Type,
		&content,
		&refID,
		&timestamp,
	)
	if err != nil {
		log.Printf("failed to insert feed item: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to add feed item")
	}

	createdItem.Content = content.String
	createdItem.RefId = refID.String
	createdItem.Timestamp = timestamp.Format(time.RFC3339)

	log.Printf("Added feed item %s for account %s (type: %s)", createdItem.GetId(), createdItem.GetAccountId(), createdItem.GetType())

	return &createdItem, nil
}

func (s *server) ListFeedItems(ctx context.Context, req *feedpb.ListFeedItemsRequest) (*feedpb.FeedItems, error) {
	log.Printf("Received ListFeedItems request: %+v", req)

	query := `SELECT id, account_id, type, content, ref_id, timestamp
			  FROM feed_items WHERE account_id = $1`
	args := []interface{}{req.GetAccountId()}
	argIndex := 2

	// Add pagination
	if req.GetBeforeId() != "" {
		// Get the timestamp of the before_id feed item
		var beforeTimestamp time.Time
		err := s.db.QueryRowContext(ctx, "SELECT timestamp FROM feed_items WHERE id = $1", req.GetBeforeId()).Scan(&beforeTimestamp)
		if err != nil {
			if err == sql.ErrNoRows {
				// If before_id not found, just list from the beginning
				log.Printf("before_id feed item not found: %s, listing from beginning", req.GetBeforeId())
			} else {
				log.Printf("failed to get timestamp for before_id: %v", err)
				return nil, status.Errorf(codes.Internal, "failed to list feed items")
			}
		} else {
			query += fmt.Sprintf(` AND timestamp < $%d`, argIndex)
			args = append(args, beforeTimestamp)
			argIndex++
		}
	}

	query += ` ORDER BY timestamp DESC`

	if req.GetLimit() > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, argIndex)
		args = append(args, req.GetLimit())
		argIndex++
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("failed to list feed items: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list feed items")
	}
	defer rows.Close()

	var feedItems []*feedpb.FeedItem
	for rows.Next() {
		var item feedpb.FeedItem
		var content sql.NullString
		var refID sql.NullString
		var timestamp time.Time

		if err := rows.Scan(
			&item.Id,
			&item.AccountId,
			&item.Type,
			&content,
			&refID,
			&timestamp,
		); err != nil {
			log.Printf("failed to scan feed item row: %v", err)
			return nil, status.Errorf(codes.Internal, "failed to list feed items")
		}

		item.Content = content.String
		item.RefId = refID.String
		item.Timestamp = timestamp.Format(time.RFC3339)

		feedItems = append(feedItems, &item)
	}

	if err := rows.Err(); err != nil {
		log.Printf("rows error during listing feed items: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to list feed items")
	}

	return &feedpb.FeedItems{Items: feedItems}, nil
}

func (s *server) GetFeedItemsByID(ctx context.Context, req *feedpb.FeedItemIDs) (*feedpb.FeedItems, error) {
	log.Printf("Received GetFeedItemsByID request: %+v", req)

	if len(req.GetIds()) == 0 {
		return &feedpb.FeedItems{Items: []*feedpb.FeedItem{}}, nil
	}

	// Build the query with an IN clause
	query := `SELECT id, account_id, type, content, ref_id, timestamp FROM feed_items WHERE id IN (`
	args := []interface{}{}
	for i, id := range req.GetIds() {
		query += fmt.Sprintf(`$%d`, i+1)
		args = append(args, id)
		if i < len(req.GetIds())-1 {
			query += `,`
		}
	}
	query += `) ORDER BY timestamp DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("failed to get feed items by IDs: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get feed items")
	}
	defer rows.Close()

	var feedItems []*feedpb.FeedItem
	for rows.Next() {
		var item feedpb.FeedItem
		var content sql.NullString
		var refID sql.NullString
		var timestamp time.Time

		if err := rows.Scan(
			&item.Id,
			&item.AccountId,
			&item.Type,
			&content,
			&refID,
			&timestamp,
		); err != nil {
			log.Printf("failed to scan feed item row by IDs: %v", err)
			return nil, status.Errorf(codes.Internal, "failed to get feed items")
		}

		item.Content = content.String
		item.RefId = refID.String
		item.Timestamp = timestamp.Format(time.RFC3339)

		feedItems = append(feedItems, &item)
	}

	if err := rows.Err(); err != nil {
		log.Printf("rows error during getting feed items by IDs: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to get feed items")
	}

	return &feedpb.FeedItems{Items: feedItems}, nil
}

// Implement HTTP handlers here

func (s *server) listFeedItemsHandler(c echo.Context) error {
	accountID := c.QueryParam("account_id")
	if accountID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "account_id query parameter is required"})
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

	req := &feedpb.ListFeedItemsRequest{
		AccountId: accountID,
		Limit:     limit,
		BeforeId:  beforeID,
	}

	feedItemsList, err := s.ListFeedItems(c.Request().Context(), req)
	if err != nil {
		// Handle gRPC errors
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

	return c.JSON(http.StatusOK, feedItemsList)
}
