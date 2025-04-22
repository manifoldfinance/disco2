package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	feedpb "github.com/sambacha/monzo/v2/feed"
	transactionspb "github.com/sambacha/monzo/v2/transactions"
)

type server struct {
	redisClient        *redis.Client
	transactionsClient transactionspb.TransactionsClient
	feedClient         feedpb.FeedClient
}

func main() {
	// Redis client setup
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})

	// Ping Redis to check connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")

	// Set up gRPC client for Transactions service
	transactionsConn, err := grpc.Dial("localhost:50052", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Transactions service: %v", err)
	}
	defer transactionsConn.Close()
	transactionsClient := transactionspb.NewTransactionsClient(transactionsConn)

	// Set up gRPC client for Feed service
	feedConn, err := grpc.Dial("localhost:50055", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Feed service: %v", err)
	}
	defer feedConn.Close()
	feedClient := feedpb.NewFeedClient(feedConn)

	s := &server{
		redisClient:        rdb,
		transactionsClient: transactionsClient,
		feedClient:         feedClient,
	}

	// Start Redis event consumer
	go s.startEventConsumer(ctx)

	// Keep main goroutine alive
	select {}
}

func (s *server) startEventConsumer(ctx context.Context) {
	log.Println("Starting Redis event consumer...")

	consumerGroup := "feed-generator-consumer-group"
	streamName := "transaction:created"

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
			Consumer: "feed-generator-instance-1",
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
					Id string `json:"id"`
				}
				if err := json.Unmarshal([]byte(payload), &event); err != nil {
					log.Printf("failed to unmarshal event payload for message %s: %v", message.ID, err)
					// Acknowledge the message to prevent reprocessing
					s.redisClient.XAck(ctx, streamName, consumerGroup, message.ID)
					continue
				}

				log.Printf("Processing transaction created event for transaction ID: %s", event.Id)

				// Attempt to generate feed item for the transaction
				if err := s.generateFeedItemForTransaction(ctx, event.Id); err != nil {
					log.Printf("failed to generate feed item for transaction %s: %v", event.Id, err)
					// Do NOT acknowledge the message, it will be retried later
					continue
				}

				// Acknowledge the message after successful processing
				if _, err := s.redisClient.XAck(ctx, streamName, consumerGroup, message.ID).Result(); err != nil {
					log.Printf("failed to acknowledge message %s: %v", message.ID, err)
				} else {
					log.Printf("Acknowledged message %s", message.ID)
				}
			}
		}
	}
}

func (s *server) generateFeedItemForTransaction(ctx context.Context, transactionID string) error {
	log.Printf("Attempting to generate feed item for transaction: %s", transactionID)

	// 1. Get transaction details from Transactions service
	txnReq := &transactionspb.TransactionQuery{Id: transactionID}
	txn, err := s.transactionsClient.GetTransaction(ctx, txnReq)
	if err != nil {
		log.Printf("failed to get transaction %s from Transactions service: %v", transactionID, err)
		return fmt.Errorf("failed to get transaction: %w", err)
	}

	// 2. Compose feed item content (e.g., "Spent $X at Y")
	// Use merchant name if available, otherwise raw name
	merchantName := txn.GetMerchantName()
	if merchantName == "" {
		merchantName = txn.GetMerchantRaw()
	}
	if merchantName == "" {
		merchantName = "an unknown place"
	}

	// Format amount (assuming cents)
	amount := float64(txn.GetAmount()) / 100.0
	content := fmt.Sprintf("Spent %.2f %s at %s", amount, txn.GetCurrency(), merchantName)

	// 3. Add feed item to Feed service
	addFeedItemReq := &feedpb.AddFeedItemRequest{
		AccountId: txn.GetAccountId(),
		Type:      "TRANSACTION",
		Content:   content,
		RefId:     transactionID,
		Timestamp: txn.GetTimestamp(),
	}
	feedItem, err := s.feedClient.AddFeedItem(ctx, addFeedItemReq)
	if err != nil {
		log.Printf("failed to add feed item for transaction %s: %v", transactionID, err)
		return fmt.Errorf("failed to add feed item: %w", err)
	}

	log.Printf("Generated and added feed item %s for transaction %s", feedItem.GetId(), transactionID)

	// 4. Publish "feed.item.created" event to Redis
	eventPayload := fmt.Sprintf(`{"feed_item_id": "%s", "account_id": "%s", "type": "%s", "transaction_id": "%s"}`,
		feedItem.GetId(),
		feedItem.GetAccountId(),
		feedItem.GetType(),
		transactionID,
	)
	if _, err := s.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: "feed:item.created",
		MaxLen: 0, // No limit
		Values: map[string]interface{}{
			"payload": eventPayload,
		},
	}).Result(); err != nil {
		log.Printf("failed to publish feed:item.created event for feed item %s: %v", feedItem.GetId(), err)
	} else {
		log.Printf("Published feed:item.created event for feed item %s", feedItem.GetId())
	}

	return nil // Successfully generated and added feed item
}
