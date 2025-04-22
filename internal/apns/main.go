package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/labstack/echo/v4"
	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	feedpb "github.com/manifoldfinance/disco2/v2/feed"
)

type server struct {
	db          *sql.DB
	redisClient *redis.Client
	feedClient  feedpb.FeedClient
	apnsClient  *apns2.Client
}

func main() {
	// APNS Configuration from environment variables
	authKeyPath := os.Getenv("APNS_KEY_PATH")
	keyID := os.Getenv("APNS_KEY_ID")
	teamID := os.Getenv("APNS_TEAM_ID")
	topic := os.Getenv("APNS_TOPIC") // Your app's bundle ID

	if authKeyPath == "" || keyID == "" || teamID == "" || topic == "" {
		log.Fatalf("APNS configuration environment variables (APNS_KEY_PATH, APNS_KEY_ID, APNS_TEAM_ID, APNS_TOPIC) must be set")
	}

	authKey, err := token.AuthKeyFromFile(authKeyPath)
	if err != nil {
		log.Fatalf("failed to load APNS auth key from %s: %v", authKeyPath, err)
	}

	apnsToken := &token.Token{
		AuthKey: authKey,
		KeyID:   keyID,
		TeamID:  teamID,
	}

	// Use Development environment by default, change to apns2.Production if needed
	apnsClient := apns2.NewTokenClient(apnsToken).Development()
	log.Println("APNS client initialized for development environment")

	// Database connection setup
	db, err := sql.Open("postgres", "user=user dbname=apns sslmode=disable")
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	// Redis client setup
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})

	// Check Redis connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")

	// Apply database schema
	schemaSQL, err := os.ReadFile("apns/schema.sql")
	if err != nil {
		log.Fatalf("failed to read schema file: %v", err)
	}
	if _, err := db.Exec(string(schemaSQL)); err != nil {
		log.Fatalf("failed to execute schema: %v", err)
	}
	log.Println("Database schema applied successfully")

	// Connect to Feed service
	feedConn, err := grpc.Dial("localhost:50055", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Feed service: %v", err)
	}
	defer feedConn.Close()
	feedClient := feedpb.NewFeedClient(feedConn)

	s := &server{
		db:          db,
		redisClient: rdb,
		feedClient:  feedClient,
		apnsClient:  apnsClient, // Add APNS client to server struct
	}

	// Set up HTTP server
	e := echo.New()
	e.POST("/devices", s.registerDeviceHandler)

	// Start HTTP server
	go func() {
		if err := e.Start(":8085"); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start http server: %v", err)
		}
	}()

	// Start Redis event consumer
	go s.startEventConsumer(ctx)

	// Keep main goroutine alive
	select {}
}

func (s *server) registerDeviceHandler(c echo.Context) error {
	var req struct {
		UserId string `json:"user_id"`
		Token  string `json:"token"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if req.UserId == "" || req.Token == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "user_id and token are required"})
	}

	query := `INSERT INTO devices (user_id, token, created_at) VALUES ($1, $2, NOW())
			  ON CONFLICT (user_id, token) DO UPDATE SET created_at = NOW()
			  RETURNING device_id, user_id, token, created_at`

	var deviceID int
	var userID string
	var token string
	var createdAt time.Time

	err := s.db.QueryRowContext(c.Request().Context(), query, req.UserId, req.Token).Scan(
		&deviceID,
		&userID,
		&token,
		&createdAt,
	)
	if err != nil {
		log.Printf("failed to register device token: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to register device token"})
	}

	log.Printf("Registered device token for user %s: %s", userID, token)

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"device_id":  deviceID,
		"user_id":    userID,
		"token":      token,
		"created_at": createdAt.Format(time.RFC3339),
	})
}

func (s *server) startEventConsumer(ctx context.Context) {
	log.Println("Starting Redis event consumer...")

	consumerGroup := "apns-consumer-group"
	streamName := "feed:item.created"

	// Create consumer group if it doesn't exist
	if _, err := s.redisClient.XGroupCreateMkStream(ctx, streamName, consumerGroup, "0").Result(); err != nil {
		// Ignore BUSYGROUP error if group already exists
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			log.Fatalf("failed to create Redis consumer group %s: %v", consumerGroup, err)
		}
	}
	log.Printf("Redis consumer group '%s' created or already exists", consumerGroup)

	for {
		// Read messages from the stream using the consumer group
		messages, err := s.redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: "apns-instance-1",
			Streams:  []string{streamName, ">"},
			Count:    10,
			Block:    0,
			NoAck:    false,
		}).Result()
		if err != nil {
			log.Printf("error reading from Redis stream %s: %v", streamName, err)
			time.Sleep(time.Second) // Wait before retrying
			continue
		}

		for _, stream := range messages {
			for _, message := range stream.Messages {
				log.Printf("Received message %s from stream %s", message.ID, stream.Stream)

				// Process the message
				payload, ok := message.Values["payload"].(string)
				if !ok {
					log.Printf("message %s has no 'payload' field or it's not a string", message.ID)
					// Acknowledge the message to prevent reprocessing
					s.redisClient.XAck(ctx, streamName, consumerGroup, message.ID)
					continue
				}

				var event struct {
					FeedItemId string `json:"feed_item_id"`
					AccountId  string `json:"account_id"`
				}
				if err := json.Unmarshal([]byte(payload), &event); err != nil {
					log.Printf("failed to unmarshal event payload for message %s: %v", message.ID, err)
					// Acknowledge the message to prevent reprocessing
					s.redisClient.XAck(ctx, streamName, consumerGroup, message.ID)
					continue
				}

				log.Printf("Processing feed item event for feed item ID: %s, account ID: %s", event.FeedItemId, event.AccountId)

				// Fetch feed item details from Feed service
				feedItemReq := &feedpb.FeedItemIDs{Ids: []string{event.FeedItemId}}
				feedItemsResp, err := s.feedClient.GetFeedItemsByID(ctx, feedItemReq)
				if err != nil {
					log.Printf("failed to get feed item %s from Feed service: %v", event.FeedItemId, err)
					// Do NOT acknowledge the message, it will be retried later
					continue
				}

				if len(feedItemsResp.GetItems()) == 0 {
					log.Printf("feed item %s not found in Feed service", event.FeedItemId)
					// Acknowledge the message as we can't process it without the feed item
					s.redisClient.XAck(ctx, streamName, consumerGroup, message.ID)
					continue
				}

				feedItem := feedItemsResp.GetItems()[0]
				notificationMessage := feedItem.GetContent()

				// Fetch device tokens for the user
				deviceTokens, err := s.getDeviceTokensForUser(ctx, feedItem.GetAccountId())
				if err != nil {
					log.Printf("failed to get device tokens for user %s: %v", feedItem.GetAccountId(), err)
					// Do NOT acknowledge the message, it will be retried later
					continue
				}

				if len(deviceTokens) == 0 {
					log.Printf("no active device tokens found for user %s", feedItem.GetAccountId())
					// Acknowledge the message as there's no one to notify
					s.redisClient.XAck(ctx, streamName, consumerGroup, message.ID)
					continue
				}

				// Send push notification to each device token
				for _, token := range deviceTokens {
					if err := s.sendAPNSNotification(ctx, token, notificationMessage); err != nil {
						log.Printf("failed to send APNS notification to token %s: %v", token, err)
					} else {
						log.Printf("Successfully sent APNS notification to token %s", token)
					}
				}

				// Acknowledge the message after processing all tokens
				if _, err := s.redisClient.XAck(ctx, streamName, consumerGroup, message.ID).Result(); err != nil {
					log.Printf("failed to acknowledge message %s: %v", message.ID, err)
				} else {
					log.Printf("Acknowledged message %s", message.ID)
				}
			}
		}
	}
}

// Helper function to get device tokens for a user from the database
func (s *server) getDeviceTokensForUser(ctx context.Context, userID string) ([]string, error) {
	query := `SELECT token FROM devices WHERE user_id = $1`
	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query device tokens: %w", err)
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, fmt.Errorf("failed to scan device token row: %w", err)
		}
		tokens = append(tokens, token)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error during getting device tokens: %w", err)
	}

	return tokens, nil
}

// Helper function to delete a device token from the database
func (s *server) deleteDeviceToken(ctx context.Context, token string) error {
	query := `DELETE FROM devices WHERE token = $1`
	result, err := s.db.ExecContext(ctx, query, token)
	if err != nil {
		return fmt.Errorf("failed to delete device token %s: %w", token, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("failed to get rows affected after deleting token %s: %v", token, err)
	} else if rowsAffected == 0 {
		log.Printf("no rows affected when deleting token %s, token might not exist", token)
	} else {
		log.Printf("successfully deleted device token %s", token)
	}

	return nil
}

// Sends an APNS notification using the configured client
func (s *server) sendAPNSNotification(ctx context.Context, deviceToken, message string) error {
	notification := &apns2.Notification{
		DeviceToken: deviceToken,
		Topic:       os.Getenv("APNS_TOPIC"), // Use the same topic (bundle ID) as configured
		Payload:     payload.NewPayload().Alert(message).Badge(1).Sound("default"),
		// You can add more fields like Priority, Expiration, etc. if needed
		// Priority:    apns2.PriorityHigh,
	}

	// Send the notification
	res, err := s.apnsClient.PushWithContext(ctx, notification)
	if err != nil {
		return fmt.Errorf("failed to send APNS push: %w", err)
	}

	// Check the response
	if !res.Sent() {
		// Log detailed reason if available
		log.Printf("APNS notification not sent to %s. Reason: %s (StatusCode: %d, APNS ID: %s)",
			deviceToken, res.Reason, res.StatusCode, res.ApnsID)

		// Handle specific reasons if necessary (e.g., BadDeviceToken, Unregistered)
		if res.Reason == apns2.ReasonBadDeviceToken || res.Reason == apns2.ReasonUnregistered {
			log.Printf("Device token %s is invalid or unregistered. Attempting to remove it from the database.", deviceToken)
			// Remove the invalid token from your database
			if deleteErr := s.deleteDeviceToken(ctx, deviceToken); deleteErr != nil {
				log.Printf("failed to remove invalid device token %s from database: %v", deviceToken, deleteErr)
			}
		}
		return fmt.Errorf("notification not sent: %s", res.Reason)
	}

	log.Printf("Successfully sent APNS notification to token %s (APNS ID: %s)", deviceToken, res.ApnsID)
	return nil
}
