# Multi-stage Dockerfile for lantern (DHCP+DNS server)
# Stage 1: Builder
FROM golang:1.22-bookworm AS builder

# Install build dependencies (libpcap for gopacket/pcap support)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libpcap-dev \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary with CGO enabled for libpcap linkage
# Version info can be injected at build time with -ldflags if desired
ARG VERSION=dev
ARG BUILD_DATE
ARG VCS_REF

RUN CGO_ENABLED=1 \
    go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE} -X main.VcsRef=${VCS_REF}" \
    -o lantern \
    ./cmd/lantern

# Stage 2: Runtime
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    libpcap0.8 \
    ca-certificates \
    tzdata \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Create lantern user and group
RUN groupadd -r lantern && useradd -r -g lantern lantern

# Create necessary directories
RUN mkdir -p /etc/lantern /var/lib/lantern /var/run/lantern && \
    chown -R lantern:lantern /var/lib/lantern /var/run/lantern && \
    chmod 755 /etc/lantern /var/lib/lantern /var/run/lantern

WORKDIR /var/lib/lantern

# Copy binary from builder
COPY --from=builder --chown=lantern:lantern /build/lantern /usr/local/bin/lantern

# Copy example configuration (if provided in source)
# COPY --chown=lantern:lantern config.example.json /etc/lantern/config.example.json

# Expose DNS (53), DHCP (67), and web UI (8080) ports
EXPOSE 53/udp 53/tcp 67/udp 8080/tcp

# Define volumes for configuration and runtime data
VOLUME ["/etc/lantern", "/var/lib/lantern"]

# Set the user
USER lantern

# Entrypoint for lantern
ENTRYPOINT ["lantern"]

# Default command (serve mode with config from /etc/lantern/config.json)
CMD ["serve", "--config", "/etc/lantern/config.json"]

# Health check using the web UI port
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8080/health || exit 1

# OCI image labels
LABEL org.opencontainers.image.title="lantern" \
      org.opencontainers.image.description="DHCP and DNS server for home networks" \
      org.opencontainers.image.url="https://github.com/phubbard/lantern" \
      org.opencontainers.image.source="https://github.com/phubbard/lantern" \
      org.opencontainers.image.vendor="lantern" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.licenses="MIT"
