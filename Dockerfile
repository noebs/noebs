# Build stage - using bookworm for glibc compatibility with CGO packages
FROM golang:1.26.5-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /src/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -buildvcs=false -ldflags "-s -w" -o /usr/local/bin/noebs ./cli


# Final stage - using slim debian for runtime
FROM debian:bookworm-slim

# Install runtime dependencies + sops + age
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl wget \
    && rm -rf /var/lib/apt/lists/* \
    && wget -q https://github.com/getsops/sops/releases/download/v3.9.4/sops-v3.9.4.linux.amd64 -O /usr/local/bin/sops \
    && chmod +x /usr/local/bin/sops \
    && wget -q https://github.com/FiloSottile/age/releases/download/v1.2.0/age-v1.2.0-linux-amd64.tar.gz \
    && tar -xzf age-v1.2.0-linux-amd64.tar.gz \
    && mv age/age age/age-keygen /usr/local/bin/ \
    && rm -rf age age-v1.2.0-linux-amd64.tar.gz

RUN useradd -u 10001 -m -s /usr/sbin/nologin -U noebs

# Copy application binary
COPY --from=builder /usr/local/bin/noebs /usr/local/bin/noebs

COPY scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Create data directory
RUN mkdir -p /data /app /app/.sops /app/.secrets \
    && chown -R noebs:noebs /data /app

WORKDIR /app
USER noebs

ENTRYPOINT ["/entrypoint.sh"]
