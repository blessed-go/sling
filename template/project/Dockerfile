# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS dev
WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

RUN go install github.com/air-verse/air@latest && \
    go install github.com/go-delve/delve/cmd/dlv@latest && \
    go clean -modcache

RUN mkdir -p /tmp/go-cache /go/pkg/mod && \
    chmod -R 777 /tmp/go-cache /go && \
    chmod 1777 /tmp

ENV GOCACHE=/tmp/go-cache \
    GOTMPDIR=/tmp \
    TMPDIR=/tmp \
    PATH="/go/bin:${PATH}"

CMD ["air", "-c", ".air.toml"]


FROM golang:1.27-alpine AS builder

ARG SERVICE_NAME

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app_binary ./cmd/${SERVICE_NAME}/main.go


FROM alpine:3.21 AS prod
WORKDIR /
RUN apk --no-cache add ca-certificates

COPY --from=builder /app_binary /app_binary
EXPOSE 8080
ENTRYPOINT ["/app_binary"]


FROM golang:1.27-alpine AS migrate-builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /migrate ./cmd/migrate/main.go

FROM alpine:3.21 AS migrate
WORKDIR /
RUN apk --no-cache add ca-certificates

COPY --from=migrate-builder /migrate /migrate
ENTRYPOINT ["/migrate"]