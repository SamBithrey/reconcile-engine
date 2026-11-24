package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher publishes canonical JSON event envelopes to the event bus.
type Publisher interface {
	PublishEvent(ctx context.Context, routingKey string, envelope EventEnvelope) error
}

// DeliveryHandler processes a single AMQP delivery and is responsible for calling Ack/Nack
// or delegating failure handling via RabbitMQ.HandleDeliveryFailure.
type DeliveryHandler func(ctx context.Context, d amqp.Delivery)

// RabbitMQ manages a durable RabbitMQ connection, topology, publisher, and QoS consumers.
type RabbitMQ struct {
	conn   *amqp.Connection
	pubCh  *amqp.Channel
	pubMu  sync.Mutex
	logger *slog.Logger
}

// Connect dials RabbitMQ, configures the publisher channel, and declares the full topology.
func Connect(ctx context.Context, amqpURL string, logger *slog.Logger) (*RabbitMQ, error) {
	if strings.TrimSpace(amqpURL) == "" {
		return nil, fmt.Errorf("rabbitmq connect: AMQP URL is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	var conn *amqp.Connection
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("rabbitmq connect: %w", ctx.Err())
		}
		conn, err = amqp.Dial(amqpURL)
		if err == nil {
			break
		}
		if attempt == 5 {
			return nil, fmt.Errorf("rabbitmq connect: dial failed after %d attempts: %w", attempt, err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("rabbitmq connect: %w", ctx.Err())
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}

	pubCh, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq connect: open publish channel: %w", err)
	}

	rmq := &RabbitMQ{
		conn:   conn,
		pubCh:  pubCh,
		logger: logger,
	}
	if err := rmq.SetupTopology(ctx); err != nil {
		_ = rmq.Close()
		return nil, err
	}
	return rmq, nil
}

// SetupTopology idempotently declares all durable exchanges, primary queues, DLQ, and
// non-blocking exponential TTL retry queues.
func (r *RabbitMQ) SetupTopology(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("setup topology: %w", err)
	}

	ch, err := r.conn.Channel()
	if err != nil {
		return fmt.Errorf("setup topology: open channel: %w", err)
	}
	defer func() { _ = ch.Close() }()

	// 1. Durable Exchanges
	exchanges := []struct {
		name string
		kind string
	}{
		{ExchangeEvents, "topic"},
		{ExchangeRetry, "direct"},
		{ExchangeDLX, "topic"},
	}
	for _, ex := range exchanges {
		if err := ch.ExchangeDeclare(ex.name, ex.kind, true, false, false, false, nil); err != nil {
			return fmt.Errorf("setup topology: declare exchange %q: %w", ex.name, err)
		}
	}

	// 2. Primary Queues + DLQ bindings
	primaryQueues := []struct {
		name       string
		routingKey string
		dlqKey     string
	}{
		{QueueInvoiceUploaded, RoutingKeyInvoiceUploaded, DLQRoutingKeyInvoiceUploaded},
		{QueueInvoiceParsed, RoutingKeyInvoiceParsed, DLQRoutingKeyInvoiceParsed},
		{QueueInvoiceReconciled, RoutingKeyInvoiceReconciled, DLQRoutingKeyInvoiceReconciled},
	}

	for _, q := range primaryQueues {
		args := amqp.Table{
			"x-dead-letter-exchange":    ExchangeDLX,
			"x-dead-letter-routing-key": q.dlqKey,
		}
		if _, err := ch.QueueDeclare(q.name, true, false, false, false, args); err != nil {
			return fmt.Errorf("setup topology: declare queue %q: %w", q.name, err)
		}
		// Direct event routing key binding.
		if err := ch.QueueBind(q.name, q.routingKey, ExchangeEvents, false, nil); err != nil {
			return fmt.Errorf("setup topology: bind queue %q to %q: %w", q.name, q.routingKey, err)
		}
		// Retry return routing key binding (matched when TTL retry queues dead-letter back to reconcile.events).
		retryReturnKey := "retry.*." + q.routingKey
		if err := ch.QueueBind(q.name, retryReturnKey, ExchangeEvents, false, nil); err != nil {
			return fmt.Errorf("setup topology: bind queue %q to %q: %w", q.name, retryReturnKey, err)
		}
	}

	// 3. Dead Letter Queue capturing all poison messages / exhausted retries.
	if _, err := ch.QueueDeclare(QueueDeadLetters, true, false, false, false, nil); err != nil {
		return fmt.Errorf("setup topology: declare queue %q: %w", QueueDeadLetters, err)
	}
	if err := ch.QueueBind(QueueDeadLetters, "#", ExchangeDLX, false, nil); err != nil {
		return fmt.Errorf("setup topology: bind queue %q to DLX: %w", QueueDeadLetters, err)
	}

	// 4. Non-blocking TTL Retry Queues dead-lettering back to reconcile.events with routing key preserved.
	retryQueues := []struct {
		name string
		tier string
		ttl  int32
	}{
		{QueueRetry1s, RetryTier1s, RetryTTL1sMs},
		{QueueRetry5s, RetryTier5s, RetryTTL5sMs},
		{QueueRetry25s, RetryTier25s, RetryTTL25sMs},
	}

	eventRoutingKeys := []string{
		RoutingKeyInvoiceUploaded,
		RoutingKeyInvoiceParsed,
		RoutingKeyInvoiceReconciled,
	}

	for _, rq := range retryQueues {
		args := amqp.Table{
			"x-message-ttl":          rq.ttl,
			"x-dead-letter-exchange": ExchangeEvents,
		}
		if _, err := ch.QueueDeclare(rq.name, true, false, false, false, args); err != nil {
			return fmt.Errorf("setup topology: declare retry queue %q: %w", rq.name, err)
		}
		for _, eventKey := range eventRoutingKeys {
			bindKey := RetryRoutingKey(rq.tier, eventKey)
			if err := ch.QueueBind(rq.name, bindKey, ExchangeRetry, false, nil); err != nil {
				return fmt.Errorf("setup topology: bind retry queue %q with %q: %w", rq.name, bindKey, err)
			}
		}
	}

	return nil
}

// PublishEvent serializes and publishes a persistent JSON EventEnvelope to reconcile.events.
func (r *RabbitMQ) PublishEvent(ctx context.Context, routingKey string, envelope EventEnvelope) error {
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("publish event %q: marshal envelope: %w", routingKey, err)
	}

	pub := amqp.Publishing{
		ContentType:   "application/json",
		DeliveryMode:  amqp.Persistent,
		MessageId:     envelope.EventID,
		CorrelationId: envelope.CorrelationID,
		Timestamp:     envelope.OccurredAt,
		Type:          envelope.EventType,
		Body:          body,
		Headers: amqp.Table{
			HeaderRetryCount:         int32(0),
			HeaderOriginalRoutingKey: routingKey,
		},
	}

	r.pubMu.Lock()
	defer r.pubMu.Unlock()

	if err := r.pubCh.PublishWithContext(ctx, ExchangeEvents, routingKey, false, false, pub); err != nil {
		return fmt.Errorf("publish event %q: %w", routingKey, err)
	}
	return nil
}

// HandleDeliveryFailure inspects x-retry-count and failure permanence:
//   - Retryable with retry_count < 3: increments x-retry-count, routes to q.retry.1s/5s/25s
//     via reconcile.retry, then Acks the original delivery.
//   - Permanent failure OR retry_count >= 3: calls Nack(requeue=false) so RabbitMQ routes
//     the message immediately to reconcile.dlx -> q.dlq.dead-letters.
func (r *RabbitMQ) HandleDeliveryFailure(ctx context.Context, d amqp.Delivery, permanent bool, reason string) error {
	retryCount := ExtractRetryCount(d.Headers)
	decision := DecideRetry(retryCount, permanent)

	if decision.Action == ActionDLQ {
		r.logger.Warn("routing delivery to DLQ",
			"routing_key", d.RoutingKey,
			"message_id", d.MessageId,
			"retry_count", retryCount,
			"permanent", permanent,
			"reason", reason,
		)
		if err := d.Nack(false, false); err != nil {
			return fmt.Errorf("handle delivery failure: nack to DLQ: %w", err)
		}
		return nil
	}

	origKey := OriginalRoutingKey(d.RoutingKey, d.Headers)
	retryKey := RetryRoutingKey(decision.Tier, origKey)

	headers := cloneHeaders(d.Headers)
	headers[HeaderRetryCount] = int32(decision.NextRetryCount)
	headers[HeaderOriginalRoutingKey] = origKey
	headers[HeaderLastError] = reason

	pub := amqp.Publishing{
		ContentType:   d.ContentType,
		DeliveryMode:  amqp.Persistent,
		MessageId:     d.MessageId,
		CorrelationId: d.CorrelationId,
		Timestamp:     time.Now().UTC(),
		Type:          d.Type,
		Body:          d.Body,
		Headers:       headers,
	}

	r.pubMu.Lock()
	err := r.pubCh.PublishWithContext(ctx, ExchangeRetry, retryKey, false, false, pub)
	r.pubMu.Unlock()
	if err != nil {
		// Could not publish to retry queue; nack with requeue=true as last resort to avoid message loss.
		_ = d.Nack(false, true)
		return fmt.Errorf("handle delivery failure: publish to retry queue %q: %w", decision.RetryQueue, err)
	}

	r.logger.Info("scheduled delivery for retry",
		"original_routing_key", origKey,
		"retry_queue", decision.RetryQueue,
		"next_retry_count", decision.NextRetryCount,
		"message_id", d.MessageId,
		"reason", reason,
	)

	if err := d.Ack(false); err != nil {
		return fmt.Errorf("handle delivery failure: ack original after retry publish: %w", err)
	}
	return nil
}

// Consume consumes messages from queueName with QoS=5 and manual acknowledgements (autoAck=false).
// On ctx cancellation, it cancels the AMQP consumer and waits for all in-flight deliveries to drain.
func (r *RabbitMQ) Consume(ctx context.Context, queueName, consumerTag string, handler DeliveryHandler) error {
	ch, err := r.conn.Channel()
	if err != nil {
		return fmt.Errorf("consume %q: open channel: %w", queueName, err)
	}
	defer func() { _ = ch.Close() }()

	if err := ch.Qos(DefaultPrefetchCount, 0, false); err != nil {
		return fmt.Errorf("consume %q: set QoS(%d): %w", queueName, DefaultPrefetchCount, err)
	}

	deliveries, err := ch.Consume(
		queueName,
		consumerTag,
		false, // autoAck: false (manual acks enforced)
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume %q: register consumer: %w", queueName, err)
	}

	r.logger.Info("consumer started", "queue", queueName, "consumer_tag", consumerTag, "qos", DefaultPrefetchCount)

	var wg sync.WaitGroup
	sem := make(chan struct{}, DefaultPrefetchCount)

	cancelDone := make(chan struct{})
	go func() {
		defer close(cancelDone)
		<-ctx.Done()
		r.logger.Info("draining consumer", "queue", queueName, "consumer_tag", consumerTag)
		if cancelErr := ch.Cancel(consumerTag, false); cancelErr != nil && !errors.Is(cancelErr, amqp.ErrClosed) {
			r.logger.Warn("consumer cancel returned error", "queue", queueName, "error", cancelErr)
		}
	}()

	for d := range deliveries {
		sem <- struct{}{}
		wg.Add(1)
		delivery := d
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			// Use a detached timeout context when draining so in-flight work finishes cleanly.
			msgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			handler(msgCtx, delivery)
		}()
	}

	wg.Wait()
	if ctx.Err() != nil {
		<-cancelDone
		return nil
	}
	return fmt.Errorf("consume %q: delivery channel closed unexpectedly", queueName)
}

// HealthCheck reports whether the RabbitMQ connection is open.
func (r *RabbitMQ) HealthCheck() error {
	if r == nil || r.conn == nil || r.conn.IsClosed() {
		return fmt.Errorf("rabbitmq healthcheck: connection closed")
	}
	return nil
}

// Channel opens a new AMQP channel (primarily useful for integration test inspection).
func (r *RabbitMQ) Channel() (*amqp.Channel, error) {
	return r.conn.Channel()
}

// Close closes the publisher channel and underlying AMQP connection.
func (r *RabbitMQ) Close() error {
	r.pubMu.Lock()
	var firstErr error
	if r.pubCh != nil {
		if err := r.pubCh.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			firstErr = err
		}
	}
	r.pubMu.Unlock()
	if r.conn != nil {
		if err := r.conn.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func cloneHeaders(src amqp.Table) amqp.Table {
	dst := make(amqp.Table, len(src)+3)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
