.PHONY: up down test test-integration migrate-up migrate-down seed demo logs

COMPOSE ?= docker compose
DATABASE_URL ?= postgres://reconcile:reconcile@localhost:5432/reconcile?sslmode=disable
API_URL ?= http://localhost:8080
RABBITMQ_URL ?= amqp://reconcile:reconcile@localhost:5672/
MINIO_ENDPOINT ?= localhost:9000
MINIO_ACCESS_KEY ?= minioadmin
MINIO_SECRET_KEY ?= minioadmin
MINIO_BUCKET ?= raw-invoices

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down -v --remove-orphans

migrate-up:
	$(COMPOSE) run --rm migrate -path=/migrations -database="postgres://reconcile:reconcile@postgres:5432/reconcile?sslmode=disable" up

migrate-down:
	$(COMPOSE) run --rm migrate -path=/migrations -database="postgres://reconcile:reconcile@postgres:5432/reconcile?sslmode=disable" down -all

test:
	go test -race -count=1 ./...

test-integration:
	INTEGRATION_TEST=1 \
	DATABASE_URL=$(DATABASE_URL) \
	RABBITMQ_URL=$(RABBITMQ_URL) \
	MINIO_ENDPOINT=$(MINIO_ENDPOINT) \
	MINIO_ACCESS_KEY=$(MINIO_ACCESS_KEY) \
	MINIO_SECRET_KEY=$(MINIO_SECRET_KEY) \
	MINIO_BUCKET=$(MINIO_BUCKET) \
	go test -race -v -count=1 ./internal/repository/... ./internal/broker/... ./internal/storage/... ./cmd/api/... ./cmd/worker/...

seed:
	go run ./cmd/seed --database-url "$(DATABASE_URL)" --api-url "$(API_URL)" --wait

demo:
	@echo "==> Starting infrastructure, migrations, API, and worker..."
	$(COMPOSE) up -d --build
	@echo "==> Waiting for API health at $(API_URL)/healthz..."
	@for i in $$(seq 1 40); do \
		if curl -fsS "$(API_URL)/healthz" >/dev/null 2>&1; then \
			echo "API is healthy!"; \
			break; \
		fi; \
		if [ "$$i" -eq 40 ]; then \
			echo "Timed out waiting for API health"; \
			$(COMPOSE) logs api worker; \
			exit 1; \
		fi; \
		sleep 2; \
	done
	@echo "==> Seeding suppliers, 15 POs, 14 GRNs, and submitting 20 mixed-format invoices..."
	go run ./cmd/seed --database-url "$(DATABASE_URL)" --api-url "$(API_URL)" --wait
	@echo "==> Reconciliation metrics summary:"
	@curl -fsS "$(API_URL)/api/v1/metrics" && echo ""
	@echo ""
	@echo "Demo ready!"
	@echo "  API:        $(API_URL)"
	@echo "  RabbitMQ:   http://localhost:15672 (reconcile / reconcile)"
	@echo "  MinIO UI:   http://localhost:9001  (minioadmin / minioadmin)"

logs:
	$(COMPOSE) logs -f api worker
