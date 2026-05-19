# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY backend/go.mod backend/main.go ./
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o ecms-server .

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/ecms-server .
EXPOSE 3001
CMD ["./ecms-server"]
