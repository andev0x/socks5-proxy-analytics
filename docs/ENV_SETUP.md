# Environment Configuration Setup

This project uses environment variables for configuration management. **Credentials and sensitive data should NEVER be hardcoded.**

## Quick Start

1. **Create your `.env` file** from the template:
   ```bash
   cp .env.example .env
   ```

2. **Fill in your actual credentials** in `.env`:
   ```bash
   DB_HOST=your-postgres-host
   DB_PORT=5432
   DB_USER=your-db-user
   DB_PASSWORD=your-secure-password
   DB_NAME=socksdb
   ```

3. **Never commit `.env`** - it's in `.gitignore` for security

## Configuration Priority

The application loads configuration in this order (highest priority first):

1. **Environment Variables** (`.env` file or system env vars)
2. **YAML Config File** (`configs/config.yml`) - optional
3. **Hardcoded Defaults** (safe values only)

## Required Environment Variables

These MUST be set before running the application:

| Variable | Description | Example |
|----------|-------------|---------|
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_USER` | Database username | `postgres` |
| `DB_PASSWORD` | Database password | `secure_pass` |
| `DB_NAME` | Database name | `socksdb` |

## Optional Environment Variables

See `.env.example` for a complete list of optional variables and their defaults.

## Deployment Notes

### Docker
Set environment variables when running the container:
```bash
docker run -e DB_HOST=postgres -e DB_USER=postgres -e DB_PASSWORD=secret myapp
```

### Kubernetes
Use Secrets for sensitive data:
```yaml
env:
  - name: DB_PASSWORD
    valueFrom:
      secretKeyRef:
        name: app-secrets
        key: db-password
```

### CI/CD
Store secrets in your CI/CD provider (GitHub Actions, GitLab CI, etc.) and pass them as environment variables during build/deploy.

## Local Development

For local development:
1. Copy `.env.example` to `.env`
2. Update values for your local database
3. Run: `go run ./cmd/proxy/main.go` or `go run ./cmd/api/main.go`

The `godotenv` package will automatically load `.env` file on startup.

## Security Best Practices

✅ **DO:**
- Store passwords in environment variables or secrets management system
- Use strong, unique passwords
- Rotate credentials regularly
- Use different credentials for different environments (dev, staging, prod)
- Enable database SSL in production (`DB_SSLMODE=require`)

❌ **DON'T:**
- Commit `.env` or `config.yml` with real credentials
- Share credentials in chat, email, or version control
- Use the same credentials across environments
- Hardcode passwords in code
- Log sensitive values

## Troubleshooting

**Error: "critical: DB_HOST environment variable not set"**
- Make sure you've created `.env` file and set `DB_HOST`
- Or set it as a system environment variable: `export DB_HOST=localhost`

**Error: "critical: DB_PASSWORD environment variable not set"**
- Ensure `DB_PASSWORD` is set in `.env` or as an environment variable

**Config not loading from `.env`?**
- Make sure `.env` file is in the project root directory
- The file must be in the working directory when running the app
