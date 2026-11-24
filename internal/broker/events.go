package broker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"reconcile-engine/internal/domain"
)

const (
	// Exchanges
	ExchangeEvents = "reconcile.events"
	ExchangeRetry  = "reconcile.retry"
	ExchangeDLX    = "reconcile.dlx"

	// Primary queues
	QueueInvoiceUploaded   = "q.invoice.uploaded"
	QueueInvoiceParsed     = "q.invoice.parsed"
	QueueInvoiceReconciled = "q.invoice.reconciled"
	QueueDeadLetters       = "q.dlq.dead-letters"

	// Non-blocking TTL retry queues
	QueueRetry1s  = "q.retry.1s"
	QueueRetry5s  = "q.retry.5s"
	QueueRetry25s = "q.retry.25s"

	// Routing keys
	RoutingKeyInvoiceUploaded   = "invoice.uploaded"
	RoutingKeyInvoiceParsed     = "invoice.parsed"
	RoutingKeyInvoiceReconciled = "invoice.reconciled"

	DLQRoutingKeyInvoiceUploaded   = "dlq.invoice.uploaded"
	DLQRoutingKeyInvoiceParsed     = "dlq.invoice.parsed"
	DLQRoutingKeyInvoiceReconciled = "dlq.invoice.reconciled"

	// Retry tiers & TTLs (milliseconds)
	RetryTier1s  = "1s"
	RetryTier5s  = "5s"
	RetryTier25s = "25s"

	RetryTTL1sMs  int32 = 1000
	RetryTTL5sMs  int32 = 5000
	RetryTTL25sMs int32 = 25000

	// Consumer & retry settings
	DefaultPrefetchCount = 5
	MaxRetries           = 3

	// Headers
	HeaderRetryCount         = "x-retry-count"
	HeaderOriginalRoutingKey = "x-original-routing-key"
	HeaderLastError          = "x-last-error"
)

// EventEnvelope is the canonical JSON wire envelope for all events on reconcile.events.
type EventEnvelope struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	CorrelationID string          `json:"correlation_id"`
	Payload       json.RawMessage `json:"payload"`
}

// InvoiceUploadedPayload is emitted by cmd/api upon accepting a new invoice file.
type InvoiceUploadedPayload struct {
	InvoiceID    uuid.UUID           `json:"invoice_id"`
	FileSHA256   string              `json:"file_sha256"`
	StorageKey   string              `json:"storage_key"`
	ContentType  string              `json:"content_type"`
	SourceFormat domain.SourceFormat `json:"source_format"`
}

// InvoiceParsedPayload is emitted by ParserWorker once an invoice is persisted in PARSED state.
type InvoiceParsedPayload struct {
	InvoiceID     uuid.UUID           `json:"invoice_id"`
	SupplierID    *uuid.UUID          `json:"supplier_id,omitempty"`
	InvoiceNumber string              `json:"invoice_number"`
	PONumber      string              `json:"po_number,omitempty"`
	SourceFormat  domain.SourceFormat `json:"source_format"`
}

// InvoiceReconciledPayload is emitted by MatcherWorker after persisting 3-way match results.
type InvoiceReconciledPayload struct {
	InvoiceID          uuid.UUID            `json:"invoice_id"`
	Status             domain.InvoiceStatus `json:"status"`
	DiscrepancyCount   int                  `json:"discrepancy_count"`
	TotalVariancePence int64                `json:"total_variance_pence"`
}

// NewEnvelope wraps a typed payload in a canonical EventEnvelope with a fresh UUID and UTC timestamp.
func NewEnvelope(eventType, correlationID string, payload any) (EventEnvelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return EventEnvelope{}, fmt.Errorf("marshal event payload (%s): %w", eventType, err)
	}
	if strings.TrimSpace(correlationID) == "" {
		correlationID = "req-" + uuid.New().String()[:8]
	}
	return EventEnvelope{
		EventID:       uuid.New().String(),
		EventType:     eventType,
		OccurredAt:    time.Now().UTC(),
		CorrelationID: correlationID,
		Payload:       raw,
	}, nil
}

// RetryAction specifies whether a failed delivery should be delayed for retry or dead-lettered.
type RetryAction string

const (
	ActionRetry RetryAction = "RETRY"
	ActionDLQ   RetryAction = "DLQ"
)

// RetryDecision is the deterministic outcome of evaluating x-retry-count and failure permanence.
type RetryDecision struct {
	Action         RetryAction
	Tier           string
	RetryQueue     string
	NextRetryCount int
}

// DecideRetry determines the next hop for a failed consumer delivery:
//   - permanent failure OR retryCount >= 3 -> DLQ (Nack requeue=false)
//   - retryCount == 0 -> q.retry.1s  (next count = 1)
//   - retryCount == 1 -> q.retry.5s  (next count = 2)
//   - retryCount == 2 -> q.retry.25s (next count = 3)
func DecideRetry(retryCount int, permanent bool) RetryDecision {
	if permanent || retryCount >= MaxRetries {
		return RetryDecision{
			Action:         ActionDLQ,
			NextRetryCount: retryCount,
		}
	}
	if retryCount < 0 {
		retryCount = 0
	}
	switch retryCount {
	case 0:
		return RetryDecision{
			Action:         ActionRetry,
			Tier:           RetryTier1s,
			RetryQueue:     QueueRetry1s,
			NextRetryCount: 1,
		}
	case 1:
		return RetryDecision{
			Action:         ActionRetry,
			Tier:           RetryTier5s,
			RetryQueue:     QueueRetry5s,
			NextRetryCount: 2,
		}
	default:
		return RetryDecision{
			Action:         ActionRetry,
			Tier:           RetryTier25s,
			RetryQueue:     QueueRetry25s,
			NextRetryCount: 3,
		}
	}
}

// RetryRoutingKey encodes both the retry tier (for direct routing on reconcile.retry)
// and the original event routing key (preserved when dead-lettered back to topic exchange reconcile.events).
func RetryRoutingKey(tier, originalRoutingKey string) string {
	return fmt.Sprintf("retry.%s.%s", tier, originalRoutingKey)
}

// OriginalRoutingKey recovers the canonical event routing key from headers or a retry-prefixed key.
func OriginalRoutingKey(deliveryRoutingKey string, headers amqp.Table) string {
	if headers != nil {
		if raw, ok := headers[HeaderOriginalRoutingKey]; ok {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if strings.HasPrefix(deliveryRoutingKey, "retry.") {
		// Format: retry.<tier>.<originalRoutingKey>
		parts := strings.SplitN(deliveryRoutingKey, ".", 3)
		if len(parts) == 3 && parts[2] != "" {
			return parts[2]
		}
	}
	return deliveryRoutingKey
}

// ExtractRetryCount safely parses x-retry-count across AMQP numeric wire types.
func ExtractRetryCount(headers amqp.Table) int {
	if headers == nil {
		return 0
	}
	raw, ok := headers[HeaderRetryCount]
	if !ok || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return 0
}
