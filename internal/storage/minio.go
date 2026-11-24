package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"reconcile-engine/internal/domain"
)

const DefaultBucket = "raw-invoices"

// Store abstracts S3-compatible object storage operations for raw invoice files.
type Store interface {
	EnsureBucket(ctx context.Context) error
	PutInvoice(ctx context.Context, sha256Hex string, format domain.SourceFormat, contentType string, data []byte) (string, error)
	Get(ctx context.Context, storageKey string) ([]byte, error)
	HealthCheck(ctx context.Context) error
}

// Config holds connection settings for MinIO / S3-compatible object storage.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

// MinIOStore wraps the MinIO Go client with content-addressed invoice key conventions.
type MinIOStore struct {
	client *minio.Client
	bucket string
	now    func() time.Time
}

// NewMinIOStore initializes a MinIO client and ensures the target bucket exists.
func NewMinIOStore(ctx context.Context, cfg Config) (*MinIOStore, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("minio store: endpoint is required")
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		bucket = DefaultBucket
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio store: init client: %w", err)
	}

	s := &MinIOStore{
		client: client,
		bucket: bucket,
		now:    func() time.Time { return time.Now().UTC() },
	}
	if err := s.EnsureBucket(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// EnsureBucket verifies the configured bucket exists and creates it if absent.
func (s *MinIOStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("minio store: check bucket %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		// Handle benign concurrent creation race.
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "BucketAlreadyOwnedByYou" || errResp.Code == "BucketAlreadyExists" {
			return nil
		}
		return fmt.Errorf("minio store: create bucket %q: %w", s.bucket, err)
	}
	return nil
}

// PutInvoice uploads raw invoice bytes to raw-invoices/YYYY/MM/<sha256>.<ext>
// and returns the canonical storage_key.
func (s *MinIOStore) PutInvoice(ctx context.Context, sha256Hex string, format domain.SourceFormat, contentType string, data []byte) (string, error) {
	if len(sha256Hex) != 64 {
		return "", fmt.Errorf("minio store: invalid sha256 length %d", len(sha256Hex))
	}
	if len(data) == 0 {
		return "", fmt.Errorf("minio store: empty payload")
	}

	key := BuildStorageKey(s.now(), sha256Hex, format)
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = FormatContentType(format)
	}

	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: ct,
	})
	if err != nil {
		return "", fmt.Errorf("minio store: put object %q: %w", key, err)
	}
	return key, nil
}

// Get fetches raw invoice bytes from object storage by its storage_key.
func (s *MinIOStore) Get(ctx context.Context, storageKey string) ([]byte, error) {
	key := strings.TrimSpace(storageKey)
	if key == "" {
		return nil, fmt.Errorf("minio store: storage_key is required")
	}

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("minio store: get object %q: %w", key, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("minio store: read object %q: %w", key, err)
	}
	return data, nil
}

// HealthCheck verifies connectivity to MinIO and presence of the target bucket.
func (s *MinIOStore) HealthCheck(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("minio healthcheck: %w", err)
	}
	if !exists {
		return fmt.Errorf("minio healthcheck: bucket %q does not exist", s.bucket)
	}
	return nil
}

// BuildStorageKey generates the canonical SHA-256 keyed path: raw-invoices/YYYY/MM/<sha256>.<ext>.
func BuildStorageKey(now time.Time, sha256Hex string, format domain.SourceFormat) string {
	utc := now.UTC()
	return fmt.Sprintf("raw-invoices/%04d/%02d/%s.%s",
		utc.Year(), int(utc.Month()), strings.ToLower(strings.TrimSpace(sha256Hex)), FormatExtension(format))
}

// FormatExtension returns the canonical file extension for a SourceFormat.
func FormatExtension(format domain.SourceFormat) string {
	switch format {
	case domain.FormatUBLXML:
		return "xml"
	case domain.FormatPDF:
		return "pdf"
	case domain.FormatCSV:
		return "csv"
	default:
		return "bin"
	}
}

// FormatContentType returns the canonical MIME content-type for a SourceFormat.
func FormatContentType(format domain.SourceFormat) string {
	switch format {
	case domain.FormatUBLXML:
		return "application/xml"
	case domain.FormatPDF:
		return "application/pdf"
	case domain.FormatCSV:
		return "text/csv"
	default:
		return "application/octet-stream"
	}
}
