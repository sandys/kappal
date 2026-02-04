# Multi-stage Dockerfile for Kappal
# Stage 1: Build
FROM golang:1.22-bookworm AS builder

WORKDIR /workspace

# Cache dependencies
COPY go.mod go.sum* ./
RUN go mod download || true

# Copy source and build
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -ldflags="-s -w" -o /kappal ./cmd/kappal

# Stage 2: Runtime (with Docker CLI for K3s management)
FROM docker:24-cli

# Install required tools including kubectl
RUN apk add --no-cache \
    bash \
    curl \
    jq && \
    curl -LO "https://dl.k8s.io/release/v1.29.0/bin/linux/amd64/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/

# Copy kappal binary
COPY --from=builder /kappal /usr/local/bin/kappal

# Set working directory
WORKDIR /project

ENTRYPOINT ["kappal"]
