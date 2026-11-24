package broker

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"reconcile-engine/internal/domain"
)

func TestDecideRetry(t *testing.T) {
	tests := []struct {
		name       string
		retryCount int
		permanent  bool
		wantAction RetryAction
		wantTier   string
		wantQueue  string
		wantNext   int
	}{
		{
			name:       "first failure routes to 1s retry queue",
			retryCount: 0,
			permanent:  false,
			wantAction: ActionRetry,
			wantTier:   RetryTier1s,
			wantQueue:  QueueRetry1s,
			wantNext:   1,
		},
		{
			name:       "second failure routes to 5s retry queue",
			retryCount: 1,
			permanent:  false,
			wantAction: ActionRetry,
			wantTier:   RetryTier5s,
			wantQueue:  QueueRetry5s,
			wantNext:   2,
		},
		{
			name:       "third failure routes to 25s retry queue",
			retryCount: 2,
			permanent:  false,
			wantAction: ActionRetry,
			wantTier:   RetryTier25s,
			wantQueue:  QueueRetry25s,
			wantNext:   3,
		},
		{
			name:       "fourth failure exhausts retries and routes to DLQ",
			retryCount: 3,
			permanent:  false,
			wantAction: ActionDLQ,
			wantNext:   3,
		},
		{
			name:       "retry count above max routes to DLQ",
			retryCount: 7,
			permanent:  false,
			wantAction: ActionDLQ,
			wantNext:   7,
		},
		{
			name:       "permanent parse error bypasses retries directly to DLQ",
			retryCount: 0,
			permanent:  true,
			wantAction: ActionDLQ,
			wantNext:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideRetry(tc.retryCount, tc.permanent)
			if got.Action != tc.wantAction || got.Tier != tc.wantTier || got.RetryQueue != tc.wantQueue || got.NextRetryCount != tc.wantNext {
				t.Fatalf("DecideRetry(%d, %v) = %+v, want {Action:%s Tier:%s Queue:%s Next:%d}",
					tc.retryCount, tc.permanent, got, tc.wantAction, tc.wantTier, tc.wantQueue, tc.wantNext)
			}
		})
	}
}

func TestExtractRetryCountAndRoutingKey(t *testing.T) {
	if got := ExtractRetryCount(nil); got != 0 {
		t.Fatalf("ExtractRetryCount(nil) = %d, want 0", got)
	}
	if got := ExtractRetryCount(amqp.Table{HeaderRetryCount: int32(2)}); got != 2 {
		t.Fatalf("ExtractRetryCount(int32(2)) = %d, want 2", got)
	}
	if got := ExtractRetryCount(amqp.Table{HeaderRetryCount: int64(3)}); got != 3 {
		t.Fatalf("ExtractRetryCount(int64(3)) = %d, want 3", got)
	}
	if got := ExtractRetryCount(amqp.Table{HeaderRetryCount: "1"}); got != 1 {
		t.Fatalf("ExtractRetryCount(\"1\") = %d, want 1", got)
	}

	rk := RetryRoutingKey(RetryTier5s, RoutingKeyInvoiceUploaded)
	if rk != "retry.5s.invoice.uploaded" {
		t.Fatalf("RetryRoutingKey = %q, want retry.5s.invoice.uploaded", rk)
	}
	if orig := OriginalRoutingKey(rk, nil); orig != RoutingKeyInvoiceUploaded {
		t.Fatalf("OriginalRoutingKey(%q) = %q, want %q", rk, orig, RoutingKeyInvoiceUploaded)
	}
	if orig := OriginalRoutingKey("retry.1s.invoice.parsed", amqp.Table{HeaderOriginalRoutingKey: RoutingKeyInvoiceParsed}); orig != RoutingKeyInvoiceParsed {
		t.Fatalf("OriginalRoutingKey(header) = %q, want %q", orig, RoutingKeyInvoiceParsed)
	}
}

func TestNewEnvelope(t *testing.T) {
	invID := uuid.New()
	env, err := NewEnvelope(RoutingKeyInvoiceUploaded, "req-test-1", InvoiceUploadedPayload{
		InvoiceID:    invID,
		FileSHA256:   "abc",
		StorageKey:   "raw-invoices/2026/11/abc.xml",
		ContentType:  "application/xml",
		SourceFormat: domain.FormatUBLXML,
	})
	if err != nil {
		t.Fatalf("NewEnvelope() error: %v", err)
	}
	if env.EventID == "" || env.EventType != RoutingKeyInvoiceUploaded || env.CorrelationID != "req-test-1" || env.OccurredAt.IsZero() {
		t.Fatalf("unexpected envelope metadata: %+v", env)
	}
	var decoded InvoiceUploadedPayload
	if err := json.Unmarshal(env.Payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.InvoiceID != invID {
		t.Fatalf("decoded InvoiceID = %s, want %s", decoded.InvoiceID, invID)
	}
}

func TestRabbitMQ_RetryAndDLQ_Integration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run RabbitMQ retry/DLQ integration test")
	}

	amqpURL := os.Getenv("RABBITMQ_URL")
	if amqpURL == "" {
		amqpURL = "amqp://reconcile:reconcile@localhost:5672/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rmq, err := Connect(ctx, amqpURL, nil)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer func() { _ = rmq.Close() }()

	ch, err := rmq.Channel()
	if err != nil {
		t.Fatalf("Channel() error: %v", err)
	}
	defer func() { _ = ch.Close() }()

	// Purge reconciled queue & DLQ before test so messages are isolated.
	_, _ = ch.QueuePurge(QueueInvoiceReconciled, false)
	_, _ = ch.QueuePurge(QueueDeadLetters, false)

	// Publish an invoice.reconciled event and verify 1s TTL retry returns to q.invoice.reconciled,
	// then permanent failure routes to q.dlq.dead-letters.
	testID := uuid.New()
	env, err := NewEnvelope(RoutingKeyInvoiceReconciled, "corr-retry-test", InvoiceReconciledPayload{
		InvoiceID: testID,
		Status:    domain.StatusNeedsReview,
	})
	if err != nil {
		t.Fatalf("NewEnvelope() error: %v", err)
	}
	if err := rmq.PublishEvent(ctx, RoutingKeyInvoiceReconciled, env); err != nil {
		t.Fatalf("PublishEvent() error: %v", err)
	}

	deliveries, err := ch.Consume(QueueInvoiceReconciled, "test-retry-consumer", false, false, false, false, nil)
	if err != nil {
		t.Fatalf("Consume() error: %v", err)
	}

	// Attempt 1: fail transiently -> routed to q.retry.1s.
	select {
	case d := <-deliveries:
		if got := ExtractRetryCount(d.Headers); got != 0 {
			t.Fatalf("attempt 1 x-retry-count = %d, want 0", got)
		}
		if err := rmq.HandleDeliveryFailure(ctx, d, false, "transient db blip"); err != nil {
			t.Fatalf("HandleDeliveryFailure(transient) error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for attempt 1 delivery")
	}

	// Attempt 2: should return after ~1s TTL with x-retry-count == 1; now fail permanently -> DLQ.
	select {
	case d := <-deliveries:
		if got := ExtractRetryCount(d.Headers); got != 1 {
			t.Fatalf("attempt 2 x-retry-count = %d, want 1", got)
		}
		if err := rmq.HandleDeliveryFailure(ctx, d, true, "permanent failure test"); err != nil {
			t.Fatalf("HandleDeliveryFailure(permanent) error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for 1s TTL retry delivery")
	}

	// Verify message landed in q.dlq.dead-letters.
	dlqDeliveries, err := ch.Consume(QueueDeadLetters, "test-dlq-consumer", false, false, false, false, nil)
	if err != nil {
		t.Fatalf("Consume(DLQ) error: %v", err)
	}

	select {
	case d := <-dlqDeliveries:
		if d.RoutingKey != DLQRoutingKeyInvoiceReconciled {
			t.Fatalf("DLQ routing key = %q, want %q", d.RoutingKey, DLQRoutingKeyInvoiceReconciled)
		}
		_ = d.Ack(false)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message in q.dlq.dead-letters")
	}
}
