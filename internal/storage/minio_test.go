package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"reconcile-engine/internal/domain"
)

func TestBuildStorageKey(t *testing.T) {
	ts := time.Date(2026, time.November, 23, 14, 30, 0, 0, time.UTC)
	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	tests := []struct {
		format domain.SourceFormat
		want   string
	}{
		{domain.FormatUBLXML, "raw-invoices/2026/11/" + hash + ".xml"},
		{domain.FormatPDF, "raw-invoices/2026/11/" + hash + ".pdf"},
		{domain.FormatCSV, "raw-invoices/2026/11/" + hash + ".csv"},
	}

	for _, tc := range tests {
		got := BuildStorageKey(ts, hash, tc.format)
		if got != tc.want {
			t.Errorf("BuildStorageKey(%s) = %q, want %q", tc.format, got, tc.want)
		}
	}
}

func TestMinIOStore_Integration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run MinIO integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9000"
	}
	accessKey := os.Getenv("MINIO_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "minioadmin"
	}
	secretKey := os.Getenv("MINIO_SECRET_KEY")
	if secretKey == "" {
		secretKey = "minioadmin"
	}
	bucket := os.Getenv("MINIO_BUCKET")
	if bucket == "" {
		bucket = DefaultBucket
	}

	store, err := NewMinIOStore(ctx, Config{
		Endpoint:  endpoint,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Bucket:    bucket,
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("NewMinIOStore() error: %v", err)
	}

	if err := store.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck() error: %v", err)
	}

	payload := []byte("<Invoice>integration-test</Invoice>")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	key, err := store.PutInvoice(ctx, hexSum, domain.FormatUBLXML, "application/xml", payload)
	if err != nil {
		t.Fatalf("PutInvoice() error: %v", err)
	}

	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get(%q) error: %v", key, err)
	}
	if string(got) != string(payload) {
		t.Fatalf("Get(%q) = %q, want %q", key, string(got), string(payload))
	}
}
