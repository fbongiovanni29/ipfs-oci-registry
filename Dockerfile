# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /oci-ipfs-registry ./cmd/registry

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -u 1000 registry

WORKDIR /app

# Copy binary from builder
COPY --from=builder /oci-ipfs-registry /app/oci-ipfs-registry

# Create data directory
RUN mkdir -p /data && chown registry:registry /data

USER registry

EXPOSE 5000

VOLUME ["/data"]

ENTRYPOINT ["/app/oci-ipfs-registry"]
CMD ["--config", "/app/config.yaml"]
