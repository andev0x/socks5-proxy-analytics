# Contributing

Thank you for considering a contribution to **socks5-proxy-analytics**. This
document explains how to set up a development environment, the conventions we
follow, and how to submit changes.

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). By
participating, you agree to uphold it.

## Prerequisites

- Go 1.26.10 or newer.
- PostgreSQL 15 or newer (16+ recommended).
- `golangci-lint` v2.x for linting.
- `gofmt` and `goimports` for formatting.

## Development setup

```bash
git clone <repo-url> socks5-proxy-analytics
cd socks5-proxy-analytics

cp .env.example .env
# Edit .env with local database credentials.

# Optional: copy the example YAML config.
cp configs/config.example.yml configs/config.yml
```

Create a local database:

```bash
psql -d postgres -c "CREATE ROLE socks5 LOGIN PASSWORD 'socks5_local_dev_password';"
psql -d postgres -c "CREATE DATABASE socks5_analytics OWNER socks5;"
```

Build and run the checks:

```bash
go mod download
go build ./...
go vet ./...
go test ./...
```

Run the two processes:

```bash
go run ./cmd/proxy
go run ./cmd/api
```

## Branching and commits

- Create a feature branch from `main`: `git checkout -b feat/short-description`.
- Use conventional commit prefixes: `feat:`, `fix:`, `docs:`, `refactor:`,
  `test:`, `chore:`, `ci:`.
- Keep commits focused and atomic. Squash fixups before opening a pull request.
- Reference related issues in commit messages and PR descriptions.

## Coding standards

- Format all Go code with `gofmt -s` (and `goimports` where available).
- Run `go vet ./...` and `golangci-lint run ./...` before submitting.
- Add or update tests for any behavior change.
- Keep public APIs documented with Go doc comments.
- Prefer small, composable functions and explicit error handling.
- Do not log credentials, tokens, or PII. Prometheus labels must be bounded
  enums only.
- The proxy forwarding path must not perform database I/O.

## Testing

Run the full suite:

```bash
go test ./...
go test -race ./...
```

The PostgreSQL integration test is skipped unless `TEST_DB_HOST` is set. A skipped
test is not a pass; set the variables when touching storage code:

```bash
TEST_DB_HOST=localhost TEST_DB_PORT=5432 TEST_DB_USER=socks5 \
  TEST_DB_PASSWORD=change_me TEST_DB_NAME=socks5_analytics_test \
  go test -v ./internal/storage
```

Benchmarks:

```bash
go test -run '^$' -bench . -benchmem -count=3 ./internal/proxy ./internal/pipeline
```

## Documentation

- Update `README.md` and relevant files under `docs/` when behavior or
  configuration changes.
- Keep claims truthful: do not document features that are not wired and tested.
- Prefer clear, concise language and logical organization.

## Pull request process

1. Ensure `gofmt -l .` produces no output and `go test ./...` passes.
2. Update tests and documentation.
3. Open a pull request against `main` with a clear title and description.
4. Describe the motivation, the change, and how it was tested.
5. Maintainers review for correctness, tests, and documentation before merging.

## Reporting bugs and requesting features

Use the issue templates under `.github/ISSUE_TEMPLATE/`. For security issues,
see [SECURITY.md](SECURITY.md) and do not open a public issue.

## License

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).