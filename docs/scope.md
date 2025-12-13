# SOCKS5 Proxy Server with Traffic Analysis (Golang)
## Project: socks5-proxy-analytics
## Author: anvndev

## Project Goals

To build a **SOCKS5 Proxy server** using **Golang** with the capability to:

* Capture and analyze network traffic
* Standardize/Normalize data
* Store data
* Provide a REST API for statistics and dashboard reporting

> **Target Outcome:** 100% Match with a Golang + Networking + Security Job Description (JD).

---

## Mandatory Modules

### 1. SOCKS5 Proxy Core (MANDATORY)

**Tasks:**

* Implement the SOCKS5 server protocol.
* Receive TCP requests from clients.
* Forward traffic to the destination server.
* Hook into the connection lifecycle to enable traffic logging.

**Logged Data:**

* Source IP
* Destination IP / Domain
* Port
* Timestamp
* Latency
* Bytes in / Bytes out
* Protocol (TCP)

**Technologies used:**

* `net` (Go standard library)
* `github.com/armon/go-socks5`
* Goroutines / Channels
* `context`

### 2. Traffic Logging & Ingest Pipeline (MANDATORY)

**Tasks:**

* Collect raw logs from the proxy module.
* Feed logs into an asynchronous processing pipeline (must not block the proxy core).

**Pipeline Components:**

* **Collector:** Receives logs from the proxy.
* **Normalizer:** Standardizes the log data structure (struct).
* **Publisher:** Stores processed data (DB / message queue).

**Technologies used:**

* Go Channels / Worker Pool implementation
* Structs and JSON marshalling
* Batch processing techniques

### 3. Data Storage (MANDATORY)

**Tasks:**

* Design the database schema for traffic logs.
* Implement data storage.
* Optimize query performance and indexing.

**Basic Schema (`traffic_logs` table):**

| Column | Data Type | Description |
| :--- | :--- | :--- |
| `timestamp` | Timestamp | Time of the traffic event. |
| `source_ip` | Text | Client's IP address. |
| `dest_ip` | Text | Destination IP. |
| `domain` | Text | Resolved destination domain. |
| `latency` | Int/Float | Connection latency. |
| `bytes_in / bytes_out` | BigInt | Data transferred. |

**Technologies used:**

* PostgreSQL
* SQL Migration tools
* Repository Pattern implementation
* Indexing (btree, etc.)

### 4. REST API / Dashboard API (MANDATORY)

**Tasks:**

* Develop a RESTful API layer to query and retrieve traffic data.

**Suggested API Endpoints:**

* `/stats/top-domains`
* `/stats/traffic`
* `/stats/latency`
* `/stats/source-ips`

**Technologies used:**

* Gin / Fiber (or similar framework)
* RESTful API principles
* JSON data format
* Pagination implementation

### 5. Configuration & Logging (MANDATORY)

**Tasks:**

* Load configuration settings from `.env` or YAML files.
* Implement unified structured logging for both the proxy and API components.

**Technologies used:**

* Viper / godotenv
* Zerolog / Zap

---

## Highly Recommended (Strong Bonus Points)

### 6. Performance & Scalability

* Implement a Worker Pool for the ingestion pipeline.
* Use Batch Insert operations for the database publisher.
* Implement limits for maximum concurrent connections.

**Tech:** `sync.Pool`, `context`, Connection Pooling.

### 7. Metrics & Monitoring

* Track and expose operational metrics (e.g., connection count, QPS, average latency).

**Tech:** Prometheus client, `/metrics` endpoint.

### 8. Security Features

* Implement SOCKS5 authentication (username/password).
* Implement IP whitelist filtering.
* Implement basic Rate Limiting.

**Tech:** `go-socks5` auth implementation, Middleware.

### 9. Testing & Reliability

* Unit tests for the pipeline components.
* Integration tests for the database layer.
* Load tests for the proxy core.

**Tech:** `testing` package, `testcontainers-go`, Load testing tools (`hey`, `wrk`).

### 10. Deployment (MANDATORY)

**Tasks:**

* Create a multi-stage Dockerfile for the application.
* Create a `docker-compose.yml` file to run the full stack (App + DB).

**Technologies used:** Docker, Docker Compose.
