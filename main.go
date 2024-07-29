package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rollkit/centralized-sequencer/sequencing"
	sequencingGRPC "github.com/rollkit/go-sequencing/proxy/grpc"
)

const (
	defaultHost      = "localhost"
	defaultPort      = "50051"
	defaultBatchTime = time.Duration(2 * time.Second)
	defaultDA        = "http://localhost:25568"
)

func main() {
	var (
		host          string
		port          string
		listenAll     bool
		batchTime     time.Duration
		da_address    string
		da_namespace  string
		da_auth_token string
	)
	flag.StringVar(&host, "host", defaultHost, "centralized sequencer host")
	flag.StringVar(&port, "port", defaultPort, "centralized sequencer port")
	flag.BoolVar(&listenAll, "listen-all", false, "listen on all network interfaces (0.0.0.0) instead of just localhost")
	flag.DurationVar(&batchTime, "batch-time", defaultBatchTime, "time in seconds to wait before generating a new batch")
	flag.StringVar(&da_address, "da_address", defaultDA, "DA address")
	flag.StringVar(&da_namespace, "da_namespace", "", "DA namespace where the sequencer submits transactions")
	flag.StringVar(&da_auth_token, "da_auth_token", "", "auth token for the DA")

	flag.Parse()

	if listenAll {
		host = "0.0.0.0"
	}

	address := fmt.Sprintf("%s:%s", host, port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	centralizedSeq, err := sequencing.NewSequencer(da_address, da_auth_token, da_namespace, batchTime)
	if err != nil {
		log.Fatalf("Failed to create centralized sequencer: %v", err)
	}
	grpcServer := sequencingGRPC.NewServer(centralizedSeq, centralizedSeq, centralizedSeq)

	log.Println("Starting gRPC server on port 50051...")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGINT)
	<-interrupt
	fmt.Println("\nCtrl+C pressed. Exiting...")
	os.Exit(0)
}
