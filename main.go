package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rollkit/centralized-sequencer/centralized"
	sequencingGRPC "github.com/rollkit/go-sequencing/proxy/grpc"
)

const (
	defaultHost = "localhost"
	defaultPort = "50051"
)

func main() {
	var (
		host          string
		port          string
		listenAll     bool
		batchTime     int64
		da_namespace  string
		da_auth_token string
	)
	flag.StringVar(&port, "port", defaultPort, "listening port")
	flag.StringVar(&host, "host", defaultHost, "listening address")
	flag.Int64Var(&batchTime, "batch-time", 2, "time in seconds to wait before generating a new batch")
	flag.StringVar(&da_namespace, "da_namespace", "", "DA namespace where the sequencer submits transactions")
	flag.StringVar(&da_auth_token, "da_auth_token", "", "auth token for the DA")
	flag.BoolVar(&listenAll, "listen-all", false, "listen on all network interfaces (0.0.0.0) instead of just localhost")
	flag.Parse()

	if listenAll {
		host = "0.0.0.0"
	}

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	centralizedSeq, err := centralized.NewSequencer()
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
