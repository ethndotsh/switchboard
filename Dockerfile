FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY . .
RUN mkdir -p /build
COPY e2e/caddybuild /build
WORKDIR /build
RUN go mod edit -replace github.com/ethndotsh/switchboard=/src && \
    go mod tidy && \
    go build -trimpath -ldflags="-w -s" -o /usr/bin/caddy .

FROM caddy:2-alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
