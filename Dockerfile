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

# Install runtime dependencies + sops + age
RUN set -eu; \
    apt-get update; \
    apt-get install -y --no-install-recommends bash ca-certificates curl; \
    rm -rf /var/lib/apt/lists/*; \
    curl -fsSL \
      https://github.com/getsops/sops/releases/download/v3.9.4/sops-v3.9.4.linux.amd64 \
      -o /tmp/sops; \
    echo '5488e32bc471de7982ad895dd054bbab3ab91c417a118426134551e9626e4e85  /tmp/sops' | sha256sum -c -; \
    install -m 0755 /tmp/sops /usr/local/bin/sops; \
    curl -fsSL \
      https://github.com/FiloSottile/age/releases/download/v1.2.1/age-v1.2.1-linux-amd64.tar.gz \
      -o /tmp/age.tar.gz; \
    echo '7df45a6cc87d4da11cc03a539a7470c15b1041ab2b396af088fe9990f7c79d50  /tmp/age.tar.gz' | sha256sum -c -; \
    tar --extract --gzip --file /tmp/age.tar.gz --directory /tmp --no-same-owner; \
    install -m 0755 /tmp/age/age /tmp/age/age-keygen /usr/local/bin/; \
    rm -rf /tmp/sops /tmp/age /tmp/age.tar.gz

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
