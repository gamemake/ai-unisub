# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder

WORKDIR /src

# Keep the dependency layer cacheable when only application code changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ai-unisub2 ./cmd/server

FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app \
    && mkdir -p /data \
    && chown -R app:app /data

WORKDIR /app
COPY --from=builder /out/ai-unisub2 /app/ai-unisub2

USER app
VOLUME ["/data"]
EXPOSE 8080

ENV DATABASE_URL=sqlite:///data/ai-unisub2.db \
    LISTEN_ADDR=:8080

ENTRYPOINT ["/app/ai-unisub2"]
