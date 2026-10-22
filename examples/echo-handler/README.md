# QueueForge — Echo Handler Example

This example demonstrates how to implement a native Go worker using the QueueForge worker runtime.

## Features Demonstrated
1. Worker instantiation (`worker.NewWorker(...)`)
2. Handler registration (`w.RegisterHandler("echo", handlerFunc)`)
3. Permanent error classification (`domain.Permanent(...)`) for non-retryable errors
4. Context deadline and cooperative cancellation observance (`ctx.Done()`)
5. Clean JSON result marshaling
6. Graceful shutdown on SIGINT/SIGTERM

## Running the Example
```bash
# Make sure QF_DATABASE_URL points to your running PostgreSQL instance
export QF_DATABASE_URL="postgres://postgres:postgres@localhost:5435/queueforge?sslmode=disable"

go run ./examples/echo-handler/main.go
```

## Submitting an Echo Job via API
```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Authorization: Bearer <API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "task_type": "echo",
    "queue": "default",
    "payload": {
      "message": "Hello from QueueForge Echo Worker!",
      "sleep_ms": 100
    }
  }'
```
