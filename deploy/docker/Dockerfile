# syntax=docker/dockerfile:1

FROM node:24-alpine AS frontend
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY index.html tsconfig.json vite.config.ts ./
COPY src ./src
COPY public ./public
RUN npm run build

FROM golang:1.26-alpine AS builder

WORKDIR /src

# Keep the dependency layer cacheable when only application code changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /src/internal/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X ai-unisub/internal/service.Version=${VERSION}" -o /out/ai-unisub ./cmd/unisub

FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app \
    && mkdir -p /data \
    && chown -R app:app /data

WORKDIR /app
COPY --from=builder /out/ai-unisub /app/ai-unisub

USER app
VOLUME ["/data"]
EXPOSE 8080

ENV UNISUB_MODE=PRD \
    DATABASE_URL=sqlite:///data/ai-unisub.db \
    LISTEN_ADDR=:8080

ENTRYPOINT ["/app/ai-unisub"]
