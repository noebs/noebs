# Build stage - using bookworm for glibc compatibility with mattn/go-sqlite3
FROM golang:1.25-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /src/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Create empty .secrets.json for the embed directive (in cli dir where the embed is)
RUN echo '{}' > .secrets.json && echo '{}' > cli/.secrets.json
RUN CGO_ENABLED=1 go build -buildvcs=false -ldflags "-s -w" -o /usr/local/bin/noebs ./cli


# Final stage - using slim debian for runtime
FROM debian:bookworm-slim

# Install runtime dependencies + litestream + sops + age
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash sqlite3 ca-certificates curl wget \
    && rm -rf /var/lib/apt/lists/* \
    && wget -q https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64.deb \
    && dpkg -i litestream-v0.3.13-linux-amd64.deb \
    && rm litestream-v0.3.13-linux-amd64.deb \
    && wget -q https://github.com/getsops/sops/releases/download/v3.9.4/sops-v3.9.4.linux.amd64 -O /usr/local/bin/sops \
    && chmod +x /usr/local/bin/sops \
    && wget -q https://github.com/FiloSottile/age/releases/download/v1.2.0/age-v1.2.0-linux-amd64.tar.gz \
    && tar -xzf age-v1.2.0-linux-amd64.tar.gz \
    && mv age/age age/age-keygen /usr/local/bin/ \
    && rm -rf age age-v1.2.0-linux-amd64.tar.gz

# Copy application binary
COPY --from=builder /usr/local/bin/noebs /usr/local/bin/noebs

# Copy configs
COPY config.yaml /app/config.yaml
COPY scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Create data directory
RUN mkdir -p /data /app /app/.sops

WORKDIR /app

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/test || exit 1

ENTRYPOINT ["/entrypoint.sh"]
