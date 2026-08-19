FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ai-unisub ./cmd/server

FROM alpine:3.22
RUN addgroup -S unisub && adduser -S -G unisub -h /app unisub
WORKDIR /app
COPY --from=build /out/ai-unisub /usr/local/bin/ai-unisub
RUN mkdir -p /app/data && chown -R unisub:unisub /app

# Runtime defaults. Override them with docker run -e or Compose environment.
# UNISUB_MASTER_KEY is intentionally omitted and must be injected at runtime.
ENV UNISUB_PORT=8080 \
    UNISUB_DB_PATH=/app/data/unisub.db \
    UNISUB_CREDENTIAL_KEY_ID=local-v1 \
    UNISUB_ADMIN_USERNAME=admin \
    UNISUB_ADMIN_PASSWORD=admin \
    UNISUB_ADMIN_TOKEN_TTL=8h \
    UNISUB_MAX_BODY_BYTES=268435456 \
    UNISUB_SHUTDOWN_TIMEOUT=15s

USER unisub
EXPOSE ${UNISUB_PORT}
VOLUME ["/app/data"]
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${UNISUB_PORT}/healthz" || exit 1
ENTRYPOINT ["ai-unisub"]
