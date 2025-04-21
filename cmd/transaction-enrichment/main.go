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

	merchantpb "github.com/manifoldfinance/disco2/v2/merchant/merchant"
	transactionspb "github.com/manifoldfinance/disco2/v2/transactions/transactions"
)

type server struct {
	redisClient        *redis.Client
	transactionsClient transactionspb.TransactionsClient
	merchantClient     merchantpb.MerchantClient
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

	// Set up gRPC client for Merchant service
	merchantConn, err := grpc.Dial("localhost:50054", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Merchant service: %v", err)
	}
	defer merchantConn.Close()
	merchantClient := merchantpb.NewMerchantClient(merchantConn)

	s := &server{
		redisClient:        rdb,
		transactionsClient: transactionsClient,
		merchantClient:     merchantClient,
	}

	// Start Redis event consumer
	go s.startEventConsumer(ctx)

	// Keep main goroutine alive
	select {}
}

func (s *server) startEventConsumer(ctx context.Context) {
	log.Println("Starting Redis event consumer...")

	consumerGroup := "enrichment-consumer-group"
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
			Consumer: "enrichment-instance-1",
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

				// Attempt to enrich the transaction
				if err := s.enrichTransaction(ctx, event.Id); err != nil {
					log.Printf("failed to enrich transaction %s: %v", event.Id, err)
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

func (s *server) enrichTransaction(ctx context.Context, transactionID string) error {
	log.Printf("Attempting to enrich transaction: %s", transactionID)

	// 1. Get transaction details from Transactions service
	txnReq := &transactionspb.TransactionQuery{Id: transactionID}
	txn, err := s.transactionsClient.GetTransaction(ctx, txnReq)
	if err != nil {
		log.Printf("failed to get transaction %s from Transactions service: %v", transactionID, err)
		return fmt.Errorf("failed to get transaction: %w", err)
	}

	// If already enriched, skip
	if txn.GetMerchantId() != "" {
		log.Printf("transaction %s already enriched, skipping", transactionID)
		return nil // Successfully processed (already done)
	}

	// 2. Use merchant_raw to find or create merchant via Merchant service
	merchantQuery := &merchantpb.MerchantQuery{RawName: txn.GetMerchantRaw()}
	merchant, err := s.merchantClient.FindOrCreateMerchant(ctx, merchantQuery)
	if err != nil {
		log.Printf("failed to find or create merchant for raw name '%s': %v", txn.GetMerchantRaw(), err)
		return fmt.Errorf("failed to find or create merchant: %w", err)
	}

	// 3. Update transaction with merchant_id and cleaned name via Transactions service
	updateTxnReq := &transactionspb.UpdateTransactionRequest{
		Id:           transactionID,
		MerchantId:   merchant.GetMerchantId(),
		MerchantName: merchant.GetName(),
		Category:     merchant.GetCategory(),
	}
	if _, err := s.transactionsClient.UpdateTransaction(ctx, updateTxnReq); err != nil {
		log.Printf("failed to update transaction %s with merchant info: %v", transactionID, err)
		return fmt.Errorf("failed to update transaction: %w", err)
	}

	log.Printf("Successfully enriched transaction %s with merchant %s", transactionID, merchant.GetMerchantId())

	return nil
}
