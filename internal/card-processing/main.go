package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	balancepb "github.com/sambacha/monzo/v2/balance"
	cardprocessingpb "github.com/sambacha/monzo/v2/card_processing"
	cardspb "github.com/sambacha/monzo/v2/cards"
	transactionspb "github.com/sambacha/monzo/v2/transactions"
)

type server struct {
	cardprocessingpb.UnimplementedCardProcessingServer
	cardsClient        cardspb.CardsClient
	balanceClient      balancepb.BalanceClient
	transactionsClient transactionspb.TransactionsClient
}

func main() {
	// Set up gRPC client for Cards service
	cardsConn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Cards service: %v", err)
	}
	defer cardsConn.Close()
	cardsClient := cardspb.NewCardsClient(cardsConn)

	// Set up gRPC client for Balance service
	balanceConn, err := grpc.Dial("localhost:50053", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Balance service: %v", err)
	}
	defer balanceConn.Close()
	balanceClient := balancepb.NewBalanceClient(balanceConn)

	// Set up gRPC client for Transactions service
	transactionsConn, err := grpc.Dial("localhost:50052", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to Transactions service: %v", err)
	}
	defer transactionsConn.Close()
	transactionsClient := transactionspb.NewTransactionsClient(transactionsConn)

	s := &server{
		cardsClient:        cardsClient,
		balanceClient:      balanceClient,
		transactionsClient: transactionsClient,
	}

	// Set up gRPC server
	grpcServer := grpc.NewServer()
	cardprocessingpb.RegisterCardProcessingServer(grpcServer, s)

	// Start gRPC server
	lis, err := net.Listen("tcp", ":50050") // Use a different port (e.g., 50050 for card-processing)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	log.Printf("gRPC server listening on %v", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Implement gRPC methods here

func (s *server) AuthorizeCardTransaction(ctx context.Context, req *cardprocessingpb.CardAuthRequest) (*cardprocessingpb.CardAuthReply, error) {
	log.Printf("Received AuthorizeCardTransaction request: %+v", req)

	// 1. Check card status via Cards service
	getCardReq := &cardspb.GetCardRequest{CardId: req.GetCardId()}
	card, err := s.cardsClient.GetCard(ctx, getCardReq)
	if err != nil {
		// Handle errors from Cards service
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.NotFound {
			log.Printf("card not found: %s", req.GetCardId())
			return &cardprocessingpb.CardAuthReply{Approved: false, DeclineReason: "card not found"}, nil
		}
		log.Printf("failed to get card %s: %v", req.GetCardId(), err)
		return nil, status.Errorf(codes.Internal, "failed to authorize transaction")
	}

	// Check card status (e.g., FROZEN, CLOSED)
	if card.GetStatus() != "ACTIVE" {
		log.Printf("card %s is not active (status: %s)", req.GetCardId(), card.GetStatus())
		return &cardprocessingpb.CardAuthReply{Approved: false, DeclineReason: fmt.Sprintf("card is %s", strings.ToLower(card.GetStatus()))}, nil
	}

	// Assuming user_id from card is the account_id for balance/transactions
	accountID := card.GetUserId()

	// 2. Authorize debit via Balance service
	authorizeDebitReq := &balancepb.AuthorizeDebitRequest{
		AccountId: accountID,
		Amount:    req.GetAmount(),
	}
	debitResult, err := s.balanceClient.AuthorizeDebit(ctx, authorizeDebitReq)
	if err != nil {
		log.Printf("failed to authorize debit for account %s: %v", accountID, err)
		return nil, status.Errorf(codes.Internal, "failed to authorize transaction")
	}

	if !debitResult.GetSuccess() {
		log.Printf("debit not authorized for account %s: %s", accountID, debitResult.GetErrorMessage())
		return &cardprocessingpb.CardAuthReply{Approved: false, DeclineReason: debitResult.GetErrorMessage()}, nil
	}

	// 3. Record transaction via Transactions service
	recordTxnReq := &transactionspb.TransactionInput{
		AccountId:   accountID,
		CardId:      req.GetCardId(),
		Amount:      req.GetAmount(),
		Currency:    req.GetCurrency(),
		MerchantId:  req.GetMerchantId(),
		MerchantRaw: req.GetMerchantName(), // Use raw name from auth request
		Status:      "AUTHORIZED",          // Initial status
	}
	_, err = s.transactionsClient.RecordTransaction(ctx, recordTxnReq)
	if err != nil {
		log.Printf("failed to record transaction for account %s: %v", accountID, err)
		// IMPORTANT: If recording transaction fails AFTER debiting balance, we have an inconsistency.
		// In a real system, this would require a compensating transaction (credit back the debit)
		// or a saga pattern. For this spec, we'll log the error and return internal error,
		// assuming monitoring will detect the balance/transaction mismatch.
		// A more robust approach would be to record the transaction first (with pending status),
		// then debit, then update transaction status to completed. Or use a transactional outbox.
		// Sticking to the spec's implied flow for now.
		return nil, status.Errorf(codes.Internal, "failed to record transaction after debit")
	}

	log.Printf("Transaction authorized and recorded for account %s, card %s, amount %d %s",
		accountID, req.GetCardId(), req.GetAmount(), req.GetCurrency())

	// If all steps succeed, return approved
	return &cardprocessingpb.CardAuthReply{Approved: true}, nil
}
