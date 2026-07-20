# Build stage - using bookworm for glibc compatibility with CGO packages
FROM golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS builder

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /src/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -buildvcs=false -ldflags "-s -w" -o /usr/local/bin/noebs ./cli


# Final stage - using slim debian for runtime
FROM debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818

# Runtime dependencies only. Secret decryption belongs to the trusted release host.
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -u 10001 -m -s /usr/sbin/nologin -U noebs

# Copy application binary
COPY --from=builder /usr/local/bin/noebs /usr/local/bin/noebs

COPY --chmod=0555 scripts/entrypoint.sh /entrypoint.sh

# Create data directory
RUN mkdir -p /data /app /app/.secrets \
    && chown -R noebs:noebs /data /app

WORKDIR /app
USER noebs

ENTRYPOINT ["/entrypoint.sh"]
