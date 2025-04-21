// Package main is the entry point for the balance service
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"

	"github.com/sambacha/disco2/v2/internal/balance/config"
	"github.com/sambacha/disco2/v2/internal/balance/db"
	"github.com/sambacha/disco2/v2/internal/balance/service"
	pb "github.com/sambacha/disco2/v2/pkg/pb/balance"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Database connection setup
	database, err := db.Connect(cfg.DBDSN)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Run database migrations
	if err := db.RunMigrations(cfg.DBDSN, "file://migrations/balance"); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Redis client setup
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
		DB:   0, // use default DB
	})

	// Ping Redis to check connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")
	defer rdb.Close()

	// Create balance service
	balanceService := service.NewBalanceService(database, rdb)

	// Create HTTP server
	e := echo.New()
	e.GET("/balance/:account_id", createBalanceHandler(balanceService))

	// Start HTTP server in a goroutine
	httpServer := &http.Server{
		Addr:    cfg.HTTPPort,
		Handler: e,
	}
	go func() {
		log.Printf("HTTP server starting on %s", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	// Create gRPC server
	grpcServer := grpc.NewServer()
	pb.RegisterBalanceServer(grpcServer, balanceService)

	// Start gRPC server in a goroutine
	lis, err := net.Listen("tcp", cfg.GRPCPort)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", cfg.GRPCPort, err)
	}
	go func() {
		log.Printf("gRPC server listening on %s", cfg.GRPCPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve gRPC: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shut down the servers
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down servers...")

	// Shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}

	// Shutdown gRPC server
	grpcServer.GracefulStop()

	log.Println("Servers successfully shut down.")
}

// createBalanceHandler creates an HTTP handler for balance endpoint
func createBalanceHandler(svc *service.BalanceService) echo.HandlerFunc {
	return func(c echo.Context) error {
		accountID := c.Param("account_id")
		req := &pb.AccountID{AccountId: accountID}

		balanceResp, err := svc.GetBalance(c.Request().Context(), req)
		if err != nil {
			switch err.Error() {
			case "rpc error: code = NotFound desc = account not found":
				return c.JSON(http.StatusNotFound, map[string]string{"error": "account not found"})
			default:
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
		}

		return c.JSON(http.StatusOK, balanceResp)
	}
}
