package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/repository"
)

// LoadExistingManifest returns an existing FixtureManifest from outDir if all referenced
// invoice files are present on disk, enabling deterministic SHA-256 idempotent re-seeding.
func LoadExistingManifest(outDir string) (*FixtureManifest, error) {
	raw, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m FixtureManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if len(m.Invoices) == 0 {
		return nil, fmt.Errorf("empty manifest")
	}
	for _, inv := range m.Invoices {
		if _, err := os.Stat(filepath.Join(outDir, "invoices", inv.Filename)); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

// SeedDatabase idempotently upserts all suppliers, purchase orders, and goods receipts into PostgreSQL.
func SeedDatabase(ctx context.Context, databaseURL string, manifest *FixtureManifest) error {
	pool, err := repository.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("seed database connect: %w", err)
	}
	defer pool.Close()

	repo := repository.NewPostgresRepo(pool)

	for _, sup := range manifest.Suppliers {
		if err := repo.UpsertSupplier(ctx, sup); err != nil {
			return fmt.Errorf("seed supplier %s: %w", sup.SupplierCode, err)
		}
	}
	for _, po := range manifest.PurchaseOrders {
		if err := repo.UpsertPurchaseOrder(ctx, po); err != nil {
			return fmt.Errorf("seed PO %s: %w", po.PONumber, err)
		}
	}
	for _, grn := range manifest.GoodsReceipts {
		if err := repo.UpsertGoodsReceipt(ctx, grn); err != nil {
			return fmt.Errorf("seed GRN %s: %w", grn.GRNNumber, err)
		}
	}
	return nil
}

type apiUploadResp struct {
	InvoiceID    uuid.UUID            `json:"invoice_id"`
	Status       domain.InvoiceStatus `json:"status"`
	FileSHA256   string               `json:"file_sha256"`
	SourceFormat domain.SourceFormat  `json:"source_format"`
	Duplicate    bool                 `json:"duplicate"`
}

// SubmittedInvoice pairs a fixture definition with its ingested ledger ID.
type SubmittedInvoice struct {
	Meta      InvoiceFixtureMeta
	InvoiceID uuid.UUID
	Duplicate bool
}

// SubmitInvoicesToAPI uploads all generated invoice fixtures via POST /api/v1/invoices.
func SubmitInvoicesToAPI(ctx context.Context, apiURL, outDir string, manifest *FixtureManifest) ([]SubmittedInvoice, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	client := &http.Client{Timeout: 20 * time.Second}

	results := make([]SubmittedInvoice, 0, len(manifest.Invoices))
	for _, meta := range manifest.Invoices {
		filePath := filepath.Join(outDir, "invoices", meta.Filename)
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read invoice fixture %s: %w", meta.Filename, err)
		}

		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", meta.Filename)
		if err != nil {
			return nil, fmt.Errorf("create multipart field for %s: %w", meta.Filename, err)
		}
		if _, err := fw.Write(raw); err != nil {
			return nil, fmt.Errorf("write multipart payload for %s: %w", meta.Filename, err)
		}
		if err := mw.Close(); err != nil {
			return nil, fmt.Errorf("close multipart writer for %s: %w", meta.Filename, err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/invoices", &body)
		if err != nil {
			return nil, fmt.Errorf("build upload request for %s: %w", meta.Filename, err)
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("upload %s to %s: %w", meta.Filename, baseURL, err)
		}
		respBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response for %s: %w", meta.Filename, readErr)
		}
		if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("upload %s returned HTTP %d: %s", meta.Filename, resp.StatusCode, strings.TrimSpace(string(respBytes)))
		}

		var parsed apiUploadResp
		if err := json.Unmarshal(respBytes, &parsed); err != nil {
			return nil, fmt.Errorf("decode upload response for %s: %w", meta.Filename, err)
		}

		results = append(results, SubmittedInvoice{
			Meta:      meta,
			InvoiceID: parsed.InvoiceID,
			Duplicate: parsed.Duplicate,
		})
	}
	return results, nil
}

// WaitForReconciliation polls the API until all submitted invoices leave UPLOADED/PARSED states.
func WaitForReconciliation(ctx context.Context, apiURL string, submitted []SubmittedInvoice) ([]domain.InvoiceDetail, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	client := &http.Client{Timeout: 10 * time.Second}

	details := make([]domain.InvoiceDetail, len(submitted))
	settled := make([]bool, len(submitted))
	remaining := len(submitted)

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for remaining > 0 {
		for i, sub := range submitted {
			if settled[i] {
				continue
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/invoices/%s", baseURL, sub.InvoiceID), nil)
			if err != nil {
				return nil, err
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("poll invoice %s: %w", sub.InvoiceID, err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("poll invoice %s returned HTTP %d: %s", sub.InvoiceID, resp.StatusCode, string(body))
			}
			var detail domain.InvoiceDetail
			if err := json.Unmarshal(body, &detail); err != nil {
				return nil, fmt.Errorf("decode invoice detail %s: %w", sub.InvoiceID, err)
			}
			if detail.Invoice.Status != domain.StatusUploaded && detail.Invoice.Status != domain.StatusParsed {
				details[i] = detail
				settled[i] = true
				remaining--
			}
		}

		if remaining == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return details, fmt.Errorf("timed out waiting for %d invoice(s) to finish reconciliation: %w", remaining, ctx.Err())
		case <-ticker.C:
		}
	}

	return details, nil
}
