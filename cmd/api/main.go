package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"reconcile-engine/internal/broker"
	"reconcile-engine/internal/repository"
	"reconcile-engine/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := envOrDefault("PORT", "8080")
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
	srv := NewServer(repo, store, rmq, logger)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("API server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-sigCtx.Done():
		logger.Info("shutting down API server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown error", "error", err)
		}
	case err := <-errCh:
		logger.Error("API server error", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
