# ── Build ────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod ./
RUN go mod download || true
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /processor .

# ── Run ──────────────────────────────────────────────────────────
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /processor /processor
EXPOSE 8082
ENTRYPOINT ["/processor"]
