# syntax=docker/dockerfile:1.7

FROM golang:1.23-alpine AS builder

ARG SWITCHBOARD_VERSION=latest
ARG SWITCHBOARD_REPLACE=/src

WORKDIR /src
COPY go.mod go.sum ./

WORKDIR /build
COPY e2e/caddybuild/main.go /build/main.go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod init github.com/ethndotsh/switchboard/caddybuild && \
    go get github.com/caddyserver/caddy/v2@v2.8.4 && \
    if [ -n "${SWITCHBOARD_REPLACE}" ]; then \
      go mod edit -require github.com/ethndotsh/switchboard@v0.0.0 && \
      go mod edit -replace github.com/ethndotsh/switchboard=${SWITCHBOARD_REPLACE}; \
    else \
      go get github.com/ethndotsh/switchboard@${SWITCHBOARD_VERSION} && \
      go mod tidy; \
    fi && \
    go mod download

WORKDIR /src
COPY . .

WORKDIR /build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod tidy && \
    go build -trimpath -ldflags="-w -s" -o /usr/bin/caddy .

FROM caddy:2-alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
