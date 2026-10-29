## Summary

Describe the change and why it is needed.

## Changes

- 

## Testing

- [ ] `gofmt -l .` produces no output
- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] `go test -race ./...` (if concurrency touched)
- [ ] PostgreSQL integration test (if storage touched)

## Documentation

- [ ] README / docs updated
- [ ] Configuration reference updated (if applicable)

## Checklist

- [ ] No credentials or secrets committed
- [ ] No database I/O on the proxy forwarding path
- [ ] Prometheus labels are bounded enums only