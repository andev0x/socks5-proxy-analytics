# SOCKS5 Proxy Analytics - Golang Project

A complete SOCKS5 proxy server implementation in Golang with real-time traffic analysis, logging pipeline, RESTful API, and comprehensive monitoring.

## Project Features

### Core Components

1. **SOCKS5 Proxy Server**
   - Full SOCKS5 protocol implementation using `go-socks5`
   - Traffic logging hooks for every connection
   - Connection lifecycle management
   - Support for TCP connections with DNS resolution

2. **Traffic Analysis Pipeline**
   - **Collector**: Asynchronous event collection from proxy
   - **Normalizer**: Data standardization with worker pool
   - **Publisher**: Batch insert optimization to database
   - Non-blocking design for high throughput

3. **Data Storage**
   - PostgreSQL integration with GORM ORM
   - Optimized schema with btree indexing
   - Automatic migrations on startup
   - Batch insert operations for performance

4. **REST API**
   - Gin framework for fast HTTP routing
   - Dashboard endpoints for traffic analytics:
     - `/stats/top-domains` - Top visited domains
     - `/stats/source-ips` - Top source IPs
     - `/stats/traffic` - Overall traffic statistics
     - `/logs/traffic` - Traffic logs with time range filtering
   - Pagination support with limit/offset
   - Time-range filtering for analytics

5. **Security Features**
   - SOCKS5 authentication (username/password)
   - IP whitelist filtering
   - Token bucket rate limiting
   - Per-client rate limit isolation

6. **Performance & Monitoring**
   - Worker pool pattern for parallel processing
   - Connection pooling with max connection limits
   - Prometheus metrics exposure
   - Structured logging with Zap
   - Batch database operations

7. **Deployment**
   - Multi-stage Docker builds
   - Docker Compose configuration
   - Environment variable support

## Project Structure

```
.
├── cmd/
│   ├── proxy/
│   │   └── main.go           # SOCKS5 proxy server entry point
│   └── api/
│       └── main.go           # REST API server entry point
├── internal/
│   ├── config/
│   │   └── config.go         # Configuration management
│   ├── logger/
│   │   └── logger.go         # Structured logging
│   ├── models/
│   │   └── traffic.go        # Data models
│   ├── pipeline/
│   │   ├── collector.go      # Event collection
│   │   ├── normalizer.go     # Data normalization
│   │   ├── publisher.go      # Database publishing
│   │   ├── pool.go           # Worker pool & connection pooling
│   │   └── pipeline_test.go  # Pipeline tests
│   ├── storage/
│   │   ├── database.go       # Database initialization
│   │   └── repository.go     # Data access layer
│   ├── proxy/
│   │   └── server.go         # SOCKS5 server implementation
│   ├── api/
│   │   └── handle.go         # API handlers
│   ├── security/
│   │   ├── security.go       # Authentication & rate limiting
│   │   └── security_test.go  # Security tests
│   └── metrics/
│       └── metrics.go        # Prometheus metrics
├── configs/
│   └── config.yml            # Configuration file
├── deployments/
│   └── docker-compose.yml    # Docker Compose setup
├── build/
│   └── docker/
│       ├── Dockerfile.api    # API Docker image
│       └── Dockerfile.proxy  # Proxy Docker image
├── go.mod                    # Go modules
└── go.sum                    # Dependency lock file
```

## Getting Started

### Prerequisites

- Go 1.25.5+
- PostgreSQL 15+
- Docker & Docker Compose (for containerized deployment)

### Local Setup

1. **Install dependencies**
```bash
go mod download
```

2. **Configure the application**

Edit `configs/config.yml`:
```yaml
proxy:
  address: "0.0.0.0"
  port: 1080
api:
  address: "0.0.0.0"
  port: 8080
database:
  host: "localhost"
  port: 5432
  user: "admin"
  password: "admin"
  database: "socksdb"
```

Or use environment variables:
```bash
export PROXY_ADDRESS=0.0.0.0
export PROXY_PORT=1080
export API_ADDRESS=0.0.0.0
export API_PORT=8080
export DB_HOST=localhost
export DB_PORT=5432
export DB_USER=admin
export DB_PASSWORD=admin
export DB_NAME=socksdb
export LOG_LEVEL=info
```

3. **Create the database**
```bash
psql -U postgres -c "CREATE DATABASE socksdb;"
```

4. **Run the proxy server**
```bash
go run ./cmd/proxy/main.go
```

5. **Run the API server (in another terminal)**
```bash
go run ./cmd/api/main.go
```

### Docker Deployment

1. **Start services with Docker Compose**
```bash
docker-compose -f deployments/docker-compose.yml up --build
```

This will start:
- PostgreSQL database (port 5432)
- SOCKS5 proxy server (port 1080)
- REST API server (port 8080)

2. **Test the proxy**
```bash
# Configure your client to use SOCKS5 proxy at localhost:1080
curl -x socks5://localhost:1080 https://example.com
```

3. **Query the API**
```bash
# Get top domains
curl http://localhost:8080/stats/top-domains?limit=10

# Get traffic statistics
curl http://localhost:8080/stats/traffic

# Get traffic logs
curl http://localhost:8080/logs/traffic?limit=100
```

## Configuration

### Proxy Configuration
- `proxy.address` - Proxy server bind address (default: `0.0.0.0`)
- `proxy.port` - Proxy server port (default: `1080`)
- `proxy.auth.enabled` - Enable SOCKS5 authentication (default: `false`)
- `proxy.auth.username` - Username for authentication
- `proxy.auth.password` - Password for authentication
- `proxy.max_connections` - Max concurrent connections (default: `10000`)
- `proxy.ip_whitelist` - List of allowed source IPs

### API Configuration
- `api.address` - API server bind address (default: `0.0.0.0`)
- `api.port` - API server port (default: `8080`)

### Database Configuration
- `database.host` - PostgreSQL host (default: `localhost`)
- `database.port` - PostgreSQL port (default: `5432`)
- `database.user` - Database user (default: `postgres`)
- `database.password` - Database password
- `database.database` - Database name (default: `socksdb`)
- `database.sslmode` - SSL mode (default: `disable`)

### Pipeline Configuration
- `pipeline.workers` - Number of normalizer workers (default: `4`)
- `pipeline.buffer_size` - Channel buffer size (default: `10000`)
- `pipeline.batch_size` - Database batch size (default: `100`)
- `pipeline.flush_interval_ms` - Batch flush interval in ms (default: `5000`)

### Logging Configuration
- `logging.level` - Log level: `debug`, `info`, `warn`, `error` (default: `info`)
- `logging.format` - Log format: `json` or text (default: `json`)

### Rate Limiting Configuration
- `rate_limit.enabled` - Enable rate limiting (default: `false`)
- `rate_limit.requests_per_second` - Rate limit threshold (default: `100`)

## API Endpoints

### Health Check
```
GET /health
```
Returns server health status.

### Top Domains
```
GET /stats/top-domains?limit=10
```
Returns the most accessed domains.

**Query Parameters:**
- `limit` (optional): Number of results (default: 10)

**Response:**
```json
[
  {
    "domain": "google.com",
    "count": 1523,
    "total_bytes_in": 5242880,
    "total_bytes_out": 2621440,
    "avg_latency_ms": 45.2
  }
]
```

### Top Source IPs
```
GET /stats/source-ips?limit=10
```
Returns the top source IPs.

**Query Parameters:**
- `limit` (optional): Number of results (default: 10)

### Traffic Statistics
```
GET /stats/traffic?start=2025-01-01T00:00:00Z&end=2025-01-02T00:00:00Z
```
Returns overall traffic statistics for a time range.

**Query Parameters:**
- `start` (optional): Start timestamp in RFC3339 format
- `end` (optional): End timestamp in RFC3339 format

**Response:**
```json
{
  "total_connections": 10000,
  "total_bytes_in": 104857600,
  "total_bytes_out": 52428800,
  "avg_latency_ms": 50.5
}
```

### Traffic Logs
```
GET /logs/traffic?limit=100&offset=0&start=2025-01-01T00:00:00Z&end=2025-01-02T00:00:00Z
```
Returns traffic logs with pagination and filtering.

**Query Parameters:**
- `limit` (optional): Number of results per page (default: 100)
- `offset` (optional): Pagination offset (default: 0)
- `start` (optional): Start timestamp in RFC3339 format
- `end` (optional): End timestamp in RFC3339 format

**Response:**
```json
[
  {
    "id": 1,
    "source_ip": "192.168.1.1",
    "destination_ip": "8.8.8.8",
    "domain": "google.com",
    "port": 443,
    "timestamp": "2025-01-01T12:00:00Z",
    "latency_ms": 45,
    "bytes_in": 1024,
    "bytes_out": 512,
    "protocol": "tcp",
    "created_at": "2025-01-01T12:00:01Z"
  }
]
```

## Monitoring

### Prometheus Metrics

Metrics are exposed at `http://localhost:9090/metrics` (configurable).

Available metrics:
- `socks5_proxy_active_connections` - Current active proxy connections
- `socks5_proxy_total_connections` - Total connections since start
- `socks5_proxy_closed_connections` - Total closed connections
- `socks5_proxy_bytes_in_total` - Total bytes received
- `socks5_proxy_bytes_out_total` - Total bytes sent
- `socks5_proxy_latency_ms` - Connection latency distribution
- `pipeline_events_collected_total` - Events collected
- `pipeline_events_processed_total` - Events processed
- `pipeline_events_published_total` - Events published to DB
- `pipeline_processing_latency_ms` - Pipeline processing latency
- `db_query_duration_ms` - Database query duration
- `db_errors_total` - Database errors

## Testing

### Run All Tests
```bash
go test ./...
```

### Run Specific Tests
```bash
# Pipeline tests
go test -v ./internal/pipeline

# Security tests
go test -v ./internal/security
```

### Test Coverage
```bash
go test -cover ./...
```

## Performance Optimizations

1. **Batch Insert Operations**
   - Database publisher batches 100 records before inserting
   - Configurable flush interval (default 5 seconds)
   - Reduces database load and increases throughput

2. **Worker Pool**
   - Normalizer uses 4 worker goroutines by default
   - Configurable worker count
   - Prevents unbounded goroutine creation

3. **Connection Pooling**
   - Max connection limits to prevent resource exhaustion
   - Active connection tracking
   - Graceful connection rejection when limit reached

4. **Buffered Channels**
   - Pipeline uses buffered channels for asynchronous processing
   - Configurable buffer sizes
   - Prevents blocking on event collection

5. **Database Indexing**
   - B-tree indexes on frequently queried columns
   - Timestamp, source IP, and domain indexed
   - Query optimization for analytics

## Security Features

1. **Authentication**
   - Optional SOCKS5 username/password authentication
   - Configurable credentials

2. **IP Whitelisting**
   - Optional source IP filtering
   - Dynamic IP management (add/remove at runtime)

3. **Rate Limiting**
   - Token bucket rate limiting algorithm
   - Per-client rate limit isolation
   - Configurable requests per second

## Logging

Structured logging with Zap provides:
- JSON log format for easy parsing
- Configurable log levels
- Contextual error information
- Performance tracking

## Technologies Used

### Core
- **Go 1.25.5** - Language
- **GORM v1.31.1** - ORM
- **PostgreSQL** - Database

### Frameworks & Libraries
- **github.com/armon/go-socks5** - SOCKS5 protocol
- **github.com/gin-gonic/gin** - Web framework
- **github.com/spf13/viper** - Configuration management
- **go.uber.org/zap** - Structured logging
- **github.com/prometheus/client_golang** - Metrics

### Infrastructure
- **Docker** - Containerization
- **Docker Compose** - Orchestration

## [License](LICENSE)

> This project is provided as-is for educational and professional purposes.

## [Author](github.com/andev0x)

