# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

FROM alpine:3.21 AS api
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=builder /out/api /app/api
EXPOSE 8080
ENTRYPOINT ["/app/api"]

FROM alpine:3.21 AS worker
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/worker /app/worker
ENTRYPOINT ["/app/worker"]

FROM alpine:3.21 AS seed
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/seed /app/seed
ENTRYPOINT ["/app/seed"]
