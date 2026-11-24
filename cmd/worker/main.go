package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"reconcile-engine/internal/broker"
	"reconcile-engine/internal/parser"
	"reconcile-engine/internal/repository"
	"reconcile-engine/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	databaseURL := envOrDefault("DATABASE_URL", "postgres://reconcile:reconcile@localhost:5432/reconcile?sslmode=disable")
	rabbitmqURL := envOrDefault("RABBITMQ_URL", "amqp://reconcile:reconcile@localhost:5672/")
	minioEndpoint := envOrDefault("MINIO_ENDPOINT", "localhost:9000")
	minioAccessKey := envOrDefault("MINIO_ACCESS_KEY", "minioadmin")
	minioSecretKey := envOrDefault("MINIO_SECRET_KEY", "minioadmin")
	minioBucket := envOrDefault("MINIO_BUCKET", storage.DefaultBucket)
	minioUseSSL, _ := strconv.ParseBool(envOrDefault("MINIO_USE_SSL", "false"))

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startupCancel()

	pool, err := repository.Connect(startupCtx, databaseURL)
	if err != nil {
		logger.Error("failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	store, err := storage.NewMinIOStore(startupCtx, storage.Config{
		Endpoint:  minioEndpoint,
		AccessKey: minioAccessKey,
		SecretKey: minioSecretKey,
		Bucket:    minioBucket,
		UseSSL:    minioUseSSL,
	})
	if err != nil {
		logger.Error("failed to initialize MinIO store", "error", err)
		os.Exit(1)
	}

	rmq, err := broker.Connect(startupCtx, rabbitmqURL, logger)
	if err != nil {
		logger.Error("failed to connect to RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer func() { _ = rmq.Close() }()

	repo := repository.NewPostgresRepo(pool)
	router := parser.NewDefaultRouter()

	parserWorker := NewParserWorker(repo, store, router, rmq, rmq, logger.With("worker", "parser"))
	matcherWorker := NewMatcherWorker(repo, rmq, rmq, logger.With("worker", "matcher"))

	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := rmq.Consume(runCtx, broker.QueueInvoiceUploaded, "reconcile-parser-worker", parserWorker.HandleDelivery); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := rmq.Consume(runCtx, broker.QueueInvoiceParsed, "reconcile-matcher-worker", matcherWorker.HandleDelivery); err != nil {
			errCh <- err
		}
	}()

	logger.Info("worker service started", "queues", []string{broker.QueueInvoiceUploaded, broker.QueueInvoiceParsed}, "qos", broker.DefaultPrefetchCount)

	select {
	case <-runCtx.Done():
		logger.Info("received shutdown signal; draining in-flight deliveries")
	case err := <-errCh:
		logger.Error("consumer exited unexpectedly", "error", err)
		stop()
	}

	wg.Wait()
	logger.Info("worker service drained and stopped cleanly")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
