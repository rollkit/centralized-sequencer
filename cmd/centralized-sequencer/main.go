package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rollkit/centralized-sequencer/sequencing"
	sequencingGRPC "github.com/rollkit/go-sequencing/proxy/grpc"
)

const (
	defaultHost      = "localhost"
	defaultPort      = "50051"
	defaultBatchTime = 2 * time.Second
	defaultDA        = "http://localhost:26658"
)

func main() {
	var (
		host           string
		port           string
		listenAll      bool
		rollupId       string
		batchTime      time.Duration
		da_address     string
		da_namespace   string
		da_auth_token  string
		db_path        string
		metricsEnabled bool
		metricsAddress string
		maxMsgSize     int
	)

	// Define flags
	flag.StringVar(&host, "host", defaultHost, "centralized sequencer host")
	flag.StringVar(&port, "port", defaultPort, "centralized sequencer port")
	flag.BoolVar(&listenAll, "listen-all", false, "listen on all network interfaces (0.0.0.0) instead of just localhost")
	flag.StringVar(&rollupId, "rollup-id", "rollupId", "rollup id")
	flag.DurationVar(&batchTime, "batch-time", defaultBatchTime, "time in seconds to wait before generating a new batch")
	flag.StringVar(&da_address, "da_address", defaultDA, "DA address")
	flag.StringVar(&da_namespace, "da_namespace", "", "DA namespace where the sequencer submits transactions")
	flag.StringVar(&da_auth_token, "da_auth_token", "", "auth token for the DA")
	flag.StringVar(&db_path, "db_path", "", "path to the database")
	flag.BoolVar(&metricsEnabled, "metrics", false, "Enable Prometheus metrics")
	flag.StringVar(&metricsAddress, "metrics-address", ":8080", "Address to expose Prometheus metrics")
	flag.IntVar(&maxMsgSize, "max-msg-size", 4*1024*1024, "Maximum gRPC message size (in bytes)")

	flag.Parse()

	if listenAll {
		host = "0.0.0.0"
	}

	address := fmt.Sprintf("%s:%s", host, port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	namespace := make([]byte, len(da_namespace)/2)
	_, err = hex.Decode(namespace, []byte(da_namespace))
	if err != nil {
		log.Fatalf("Error decoding namespace: %v", err)
	}

	var metricsServer *http.Server
	if metricsEnabled {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		metricsServer = &http.Server{
			Addr:              metricsAddress,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Printf("Starting metrics server on %v...\n", metricsAddress)
			if err := metricsServer.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatalf("Failed to serve metrics: %v", err)
			}
		}()
	}

	metrics, err := sequencing.DefaultMetricsProvider(metricsEnabled)(da_namespace)
	if err != nil {
		log.Fatalf("Failed to create metrics: %v", err)
	}
	centralizedSeq, err := sequencing.NewSequencer(da_address, da_auth_token, namespace, []byte(rollupId), batchTime, metrics, db_path)
	if err != nil {
		log.Fatalf("Failed to create centralized sequencer: %v", err)
	}

	// Create gRPC server with max message size options
	grpcOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
	}

	grpcServer := sequencingGRPC.NewServer(centralizedSeq, centralizedSeq, centralizedSeq, grpcOpts...)

	log.Printf("Starting centralized sequencing gRPC server on %s...\n", address)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGINT)
	<-interrupt
	if metricsServer != nil {
		if err := metricsServer.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down metrics server: %v", err)
		}
	}
	fmt.Println("\nCtrl+C pressed. Exiting...")
	os.Exit(0)
}
