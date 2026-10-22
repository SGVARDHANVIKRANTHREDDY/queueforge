# Build Stage
FROM golang:1.27.3-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/admin ./cmd/admin

# Final Runtime Stage
FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/api /app/api
COPY --from=builder /bin/worker /app/worker
COPY --from=builder /bin/admin /app/admin
COPY web /app/web

# Run as an unprivileged user.
RUN adduser -D -H -u 10001 qf
USER qf

EXPOSE 8080

CMD ["/app/api"]
