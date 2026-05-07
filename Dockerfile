FROM golang:1.23-alpine AS builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server    ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker    ./cmd/worker \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/scheduler ./cmd/scheduler \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed      ./cmd/seed \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate   ./cmd/migrate

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
WORKDIR /app

COPY --from=builder /out/server    /app/server
COPY --from=builder /out/worker    /app/worker
COPY --from=builder /out/scheduler /app/scheduler
COPY --from=builder /out/seed      /app/seed
COPY --from=builder /out/migrate   /app/migrate
COPY templates  /app/templates
COPY static     /app/static
COPY migrations /app/migrations

USER app
EXPOSE 8080
ENTRYPOINT ["/app/server"]
