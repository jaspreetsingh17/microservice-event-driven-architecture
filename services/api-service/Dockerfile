# ── Build ────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod ./
RUN go mod download || true
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /api .

# ── Run ──────────────────────────────────────────────────────────
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
