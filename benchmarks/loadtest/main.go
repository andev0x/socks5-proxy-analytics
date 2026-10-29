// Command loadtest is a deterministic SOCKS5 workload generator for measuring the real
// proxy process. It performs full SOCKS5 handshakes and relays real TCP payload through a
// running proxy to a local upstream server it owns.
//
// It is intentionally not a Go benchmark: it measures end-to-end behavior (including the
// analytics pipeline and PostgreSQL, because the production proxy cannot run without them)
// and reports tail latency, throughput, failures and relay byte-accounting drift.
//
// Usage:
//
//	go run ./benchmarks/loadtest -proxy 127.0.0.1:1080 -metrics http://127.0.0.1:9090/metrics
//
// See benchmarks/README.md for the full flag reference and methodology.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	stageHandshake = "handshake"
	stageConnect   = "connect"
	stageRelay     = "relay"
	stageTimeout   = "timeout"
)

type config struct {
	proxyAddr    string
	metricsURL   string
	username     string
	password     string
	concurrency  int
	duration     time.Duration
	warmup       time.Duration
	rate         int
	payloadBytes int
	responseSize int
	hold         time.Duration
	mode         string
	domain       string
	jsonOut      string
	dialTimeout  time.Duration
	ioTimeout    time.Duration
}

type counters struct {
	attempted   atomic.Int64
	succeeded   atomic.Int64
	failed      atomic.Int64
	handshake   atomic.Int64
	connectFail atomic.Int64
	relayFail   atomic.Int64
	timeouts    atomic.Int64
	sentBytes   atomic.Int64
	recvBytes   atomic.Int64
}

type sessionResult struct {
	setupLatency time.Duration
	duration     time.Duration
	err          error
	stage        string
}

type metricsSnapshot struct {
	cpuSeconds  float64
	residentMem float64
	goroutines  float64
	relayUp     float64
	relayDown   float64
	accepted    float64
	dropped     float64
	published   float64
	publishFail float64
}

func main() {
	cfg := parseFlags()

	if err := run(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "loadtest failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config

	flag.StringVar(&cfg.proxyAddr, "proxy", "127.0.0.1:1080", "SOCKS5 proxy address")
	flag.StringVar(&cfg.metricsURL, "metrics", "http://127.0.0.1:9090/metrics",
		"proxy Prometheus endpoint (empty to skip)")
	flag.StringVar(&cfg.username, "username", "", "RFC 1929 username (empty for no-auth)")
	flag.StringVar(&cfg.password, "password", "", "RFC 1929 password")
	flag.IntVar(&cfg.concurrency, "concurrency", 32, "concurrent clients")
	flag.DurationVar(&cfg.duration, "duration", 30*time.Second, "measurement duration")
	flag.DurationVar(&cfg.warmup, "warmup", 5*time.Second, "warm-up duration excluded from results")
	flag.IntVar(&cfg.rate, "rate", 0, "connection attempts per second across all clients (0 = as fast as possible)")
	flag.IntVar(&cfg.payloadBytes, "payload", 4096, "bytes uploaded per session")
	flag.IntVar(&cfg.responseSize, "response", 4096, "bytes requested from the upstream per session")
	flag.DurationVar(&cfg.hold, "hold", 0, "extra time to hold each session open after relaying")
	flag.StringVar(&cfg.mode, "mode", "echo", "upstream behavior: echo, upload, download, idle")
	flag.StringVar(&cfg.domain, "domain", "", "request this domain instead of an IP literal (exercises DNS)")
	flag.StringVar(&cfg.jsonOut, "json", "", "write the result as JSON to this path")
	flag.DurationVar(&cfg.dialTimeout, "dial-timeout", 5*time.Second, "per-connection dial timeout")
	flag.DurationVar(&cfg.ioTimeout, "io-timeout", 30*time.Second, "per-connection I/O timeout")
	flag.Parse()

	return cfg
}

func validateConfig(cfg *config) error {
	if cfg.concurrency <= 0 {
		return errors.New("-concurrency must be greater than zero")
	}

	if cfg.payloadBytes < 0 || cfg.responseSize < 0 {
		return errors.New("-payload and -response must not be negative")
	}

	if cfg.duration <= 0 {
		return errors.New("-duration must be greater than zero")
	}

	// RFC 1929 length prefixes are one byte.
	if len(cfg.username) > 255 || len(cfg.password) > 255 {
		return errors.New("-username and -password must be at most 255 bytes")
	}

	// SOCKS5 domain length is one byte.
	if len(cfg.domain) > 255 {
		return errors.New("-domain must be at most 255 bytes")
	}

	return nil
}

func run(cfg *config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}

	upstream, err := startUpstream(cfg)
	if err != nil {
		return err
	}

	defer func() { _ = upstream.Close() }()

	// Warm-up is discarded so page faults, connection pools and DNS caches do not
	// contaminate the measurement window.
	if cfg.warmup > 0 {
		_ = drive(cfg, upstream.port, cfg.warmup)
	}

	before, err := readMetrics(cfg.metricsURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics unavailable before run: %v\n", err)
	}

	start := time.Now()
	result := drive(cfg, upstream.port, cfg.duration)
	elapsed := time.Since(start)

	after, err := readMetrics(cfg.metricsURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics unavailable after run: %v\n", err)
	}

	report := buildReport(cfg, result, elapsed, before, after, upstream)
	printReport(&report) //nolint:forbidigo // CLI output is the purpose of this command

	if cfg.jsonOut != "" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode report: %w", err)
		}

		if err := os.WriteFile(cfg.jsonOut, append(encoded, '\n'), 0o600); err != nil {
			return fmt.Errorf("write report: %w", err)
		}

		fmt.Printf("\nJSON report written to %s\n", cfg.jsonOut) //nolint:forbidigo // CLI output
	}

	return nil
}

type upstreamServer struct {
	listener net.Listener
	port     int
	mode     string
	sent     atomic.Int64
	received atomic.Int64
}

func startUpstream(cfg *config) (*upstreamServer, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen upstream: %w", err)
	}

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()

		return nil, errors.New("upstream listener is not TCP")
	}

	server := &upstreamServer{listener: listener, port: tcpAddr.Port, mode: cfg.mode}

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			go server.handle(conn, cfg)
		}
	}()

	return server, nil
}

func (u *upstreamServer) Close() error {
	return u.listener.Close()
}

//nolint:gocognit // a small, explicit protocol switch is clearer than indirection
func (u *upstreamServer) handle(conn net.Conn, cfg *config) {
	defer closeAbortively(conn)

	payload := make([]byte, cfg.payloadBytes)
	response := make([]byte, cfg.responseSize)

	switch u.mode {
	case "idle":
		_, _ = io.Copy(io.Discard, conn)

		return
	case "download":
		// Read the request, then stream the configured response size.
		if cfg.payloadBytes > 0 {
			if _, err := io.ReadFull(conn, payload); err != nil {
				return
			}

			u.received.Add(int64(len(payload)))
		}

		if _, err := conn.Write(response); err != nil {
			return
		}

		u.sent.Add(int64(len(response)))

		return
	case "discard", "upload":
		written, _ := io.Copy(io.Discard, conn)
		u.received.Add(written)

		return
	default: // echo
		for {
			n, err := conn.Read(payload)
			if n > 0 {
				u.received.Add(int64(n))

				if _, writeErr := conn.Write(payload[:n]); writeErr != nil {
					return
				}

				u.sent.Add(int64(n))
			}

			if err != nil {
				return
			}
		}
	}
}

type driveResult struct {
	counters counters
	latency  []time.Duration
}

//nolint:gocognit // worker orchestration reads better as one function
func drive(cfg *config, upstreamPort int, duration time.Duration) *driveResult {
	result := &driveResult{}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var pacer <-chan time.Time

	if cfg.rate > 0 {
		interval := time.Second / time.Duration(cfg.rate)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		pacer = ticker.C
	}

	var wg sync.WaitGroup

	results := make(chan sessionResult, cfg.concurrency*2)

	for i := 0; i < cfg.concurrency; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if pacer != nil {
					select {
					case <-ctx.Done():
						return
					case <-pacer:
					}
				}

				result.counters.attempted.Add(1)

				// Let sessions that already started finish even after the measurement window ends.
				sessionCtx, sessionCancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ioTimeout)
				session := runSession(sessionCtx, cfg, upstreamPort)
				sessionCancel()
				if session.err != nil {
					result.counters.failed.Add(1)
					incrementStage(&result.counters, session.stage)

					if session.stage == stageConnect {
						// Avoid a hot spin when the proxy is down.
						time.Sleep(10 * time.Millisecond)
					}
				} else {
					result.counters.succeeded.Add(1)
					result.counters.sentBytes.Add(int64(cfg.payloadBytes))
					result.counters.recvBytes.Add(int64(effectiveResponseBytes(cfg)))
				}

				results <- session
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for session := range results {
		if session.err == nil {
			result.latency = append(result.latency, session.setupLatency)
		}
	}

	return result
}

func incrementStage(c *counters, stage string) {
	switch stage {
	case stageHandshake:
		c.handshake.Add(1)
	case stageConnect:
		c.connectFail.Add(1)
	case stageRelay:
		c.relayFail.Add(1)
	case stageTimeout:
		c.timeouts.Add(1)
	}
}

func runSession(ctx context.Context, cfg *config, upstreamPort int) sessionResult {
	start := time.Now()

	dialer := &net.Dialer{Timeout: cfg.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.proxyAddr)
	if err != nil {
		return sessionResult{err: err, stage: stageConnect}
	}

	defer closeAbortively(conn)

	_ = conn.SetDeadline(time.Now().Add(cfg.ioTimeout))

	if err := negotiate(conn, cfg); err != nil {
		return sessionResult{err: err, stage: classify(err)}
	}

	target := "127.0.0.1"
	if cfg.domain != "" {
		target = cfg.domain
	}

	if err := sendConnect(conn, target, upstreamPort); err != nil {
		return sessionResult{err: err, stage: classify(err)}
	}

	setupLatency := time.Since(start)

	if err := relaySession(conn, cfg); err != nil {
		return sessionResult{setupLatency: setupLatency, err: err, stage: classify(err)}
	}

	if cfg.hold > 0 {
		time.Sleep(cfg.hold)
	}

	return sessionResult{setupLatency: setupLatency, duration: time.Since(start)}
}

// closeAbortively closes with SO_LINGER=0 so loopback load tests do not exhaust the
// ephemeral port range with TIME_WAIT sockets. The proxy's relay has already completed, so
// this does not change the bytes it accounts for.
func closeAbortively(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetLinger(0)
	}

	_ = conn.Close()
}

func classify(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return stageTimeout
	}

	return stageRelay
}

func negotiate(conn net.Conn, cfg *config) error {
	methods := []byte{0x00}
	if cfg.username != "" {
		methods = []byte{0x02}
	}

	greeting := append([]byte{0x05, byte(len(methods))}, methods...) // #nosec G115 -- one method is offered
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("write greeting: %w", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("read method reply: %w", err)
	}

	if reply[1] == 0xFF {
		return errors.New("proxy rejected all offered authentication methods")
	}

	if reply[1] != methods[0] {
		return fmt.Errorf("proxy selected unexpected method %d", reply[1])
	}

	if reply[1] != 0x02 {
		return nil
	}

	auth := make([]byte, 0, 2+len(cfg.username)+1+len(cfg.password))
	auth = append(auth, 0x01, byte(len(cfg.username))) // #nosec G115 -- flags are length-checked
	auth = append(auth, cfg.username...)
	auth = append(auth, byte(len(cfg.password))) // #nosec G115 -- flags are length-checked
	auth = append(auth, cfg.password...)

	if _, err := conn.Write(auth); err != nil {
		return fmt.Errorf("write auth: %w", err)
	}

	authReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, authReply); err != nil {
		return fmt.Errorf("read auth reply: %w", err)
	}

	if authReply[1] != 0x00 {
		return fmt.Errorf("authentication failed with status %d", authReply[1])
	}

	return nil
}

func sendConnect(conn net.Conn, host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("destination port %d is out of range", port)
	}

	if len(host) > 255 {
		return errors.New("destination host is longer than 255 bytes")
	}

	request := []byte{0x05, 0x01, 0x00}

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		request = append(request, 0x03, byte(len(host))) // #nosec G115 -- length checked above
		request = append(request, host...)
	}

	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port)) // #nosec G115 -- range checked above
	request = append(request, portBytes...)

	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("write connect: %w", err)
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("read reply header: %w", err)
	}

	var addressLength int

	switch header[3] {
	case 0x01:
		addressLength = net.IPv4len
	case 0x04:
		addressLength = net.IPv6len
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return fmt.Errorf("read reply domain length: %w", err)
		}

		addressLength = int(length[0])
	default:
		return fmt.Errorf("unexpected reply address type %d", header[3])
	}

	if _, err := io.ReadFull(conn, make([]byte, addressLength+2)); err != nil {
		return fmt.Errorf("read reply address: %w", err)
	}

	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS5 CONNECT failed with reply code %d", header[1])
	}

	return nil
}

func relaySession(conn net.Conn, cfg *config) error {
	if cfg.payloadBytes > 0 {
		if _, err := conn.Write(make([]byte, cfg.payloadBytes)); err != nil {
			return fmt.Errorf("write payload: %w", err)
		}
	}

	expected := effectiveResponseBytes(cfg)
	if expected > 0 {
		if _, err := io.ReadFull(conn, make([]byte, expected)); err != nil {
			return fmt.Errorf("read response: %w", err)
		}
	}

	return nil
}

// effectiveResponseBytes returns the response size the upstream will actually produce.
// Echo mirrors the upload; discard and idle produce nothing.
func effectiveResponseBytes(cfg *config) int {
	switch cfg.mode {
	case "echo":
		return cfg.payloadBytes
	case "discard", "upload", "idle":
		return 0
	default:
		return cfg.responseSize
	}
}

type report struct {
	Config         map[string]any   `json:"config"`
	Environment    map[string]any   `json:"environment"`
	ElapsedSeconds float64          `json:"elapsed_seconds"`
	Attempted      int64            `json:"attempted"`
	Succeeded      int64            `json:"succeeded"`
	Failed         int64            `json:"failed"`
	FailureStages  map[string]int64 `json:"failure_stages"`
	ConnPerSecond  float64          `json:"connections_per_second"`
	SuccessPerSec  float64          `json:"successful_connections_per_second"`
	SetupLatency   map[string]any   `json:"setup_latency_ms"`
	Payload        map[string]any   `json:"payload"`
	Accounting     map[string]any   `json:"accounting"`
	ProxyMetrics   map[string]any   `json:"proxy_metrics"`
	Notes          []string         `json:"notes"`
}

func buildReport(
	cfg *config, result *driveResult, elapsed time.Duration,
	before, after metricsSnapshot, upstream *upstreamServer,
) report {
	sort.Slice(result.latency, func(i, j int) bool { return result.latency[i] < result.latency[j] })

	successful := result.counters.succeeded.Load()

	rep := report{
		Config: map[string]any{
			"proxy":       cfg.proxyAddr,
			"concurrency": cfg.concurrency,
			"duration":    cfg.duration.String(),
			"warmup":      cfg.warmup.String(),
			"rate":        cfg.rate,
			"payload":     cfg.payloadBytes,
			"response":    cfg.responseSize,
			"hold":        cfg.hold.String(),
			"mode":        cfg.mode,
			"domain":      cfg.domain,
			"auth":        cfg.username != "",
		},
		Environment: map[string]any{
			"hostname":  hostname(),
			"goos":      runtime.GOOS,
			"goarch":    runtime.GOARCH,
			"cpus":      runtime.NumCPU(),
			"goversion": runtime.Version(),
		},
		ElapsedSeconds: elapsed.Seconds(),
		Attempted:      result.counters.attempted.Load(),
		Succeeded:      successful,
		Failed:         result.counters.failed.Load(),
		FailureStages: map[string]int64{
			"handshake": result.counters.handshake.Load(),
			"connect":   result.counters.connectFail.Load(),
			"relay":     result.counters.relayFail.Load(),
			"timeout":   result.counters.timeouts.Load(),
		},
		ConnPerSecond: float64(result.counters.attempted.Load()) / elapsed.Seconds(),
		SuccessPerSec: float64(successful) / elapsed.Seconds(),
		SetupLatency:  latencySummary(result.latency),
		Payload: map[string]any{
			"bytes_sent":              result.counters.sentBytes.Load(),
			"bytes_received":          result.counters.recvBytes.Load(),
			"sent_mb_per_s":           float64(result.counters.sentBytes.Load()) / elapsed.Seconds() / (1 << 20),
			"recv_mb_per_s":           float64(result.counters.recvBytes.Load()) / elapsed.Seconds() / (1 << 20),
			"upstream_received_bytes": upstream.received.Load(),
			"upstream_sent_bytes":     upstream.sent.Load(),
		},
		ProxyMetrics: map[string]any{
			"cpu_seconds_delta":           after.cpuSeconds - before.cpuSeconds,
			"resident_memory_bytes_after": after.residentMem,
			"resident_memory_mib_after":   after.residentMem / (1 << 20),
			"goroutines_after":            after.goroutines,
			"relay_bytes_up_delta":        after.relayUp - before.relayUp,
			"relay_bytes_down_delta":      after.relayDown - before.relayDown,
			"accepted_total_delta":        after.accepted - before.accepted,
			"events_dropped_delta":        after.dropped - before.dropped,
			"events_published_delta":      after.published - before.published,
			"publish_failures_delta":      after.publishFail - before.publishFail,
		},
	}

	rep.Accounting = accounting(result, before, after, cfg)
	rep.Notes = []string{
		"The harness, upstream and proxy share this machine; the harness CPU is not measured.",
		"Analytics cannot be disabled in the supported production configuration; events are persisted to PostgreSQL.",
		"Loopback connection churn is limited by ephemeral ports and TIME_WAIT; very high rates " +
			"may fail with EADDRNOTAVAIL. The harness closes abortively (SO_LINGER=0) to reduce this.",
		"Prometheus counter deltas are scraped immediately after the measurement window and " +
			"may exclude sessions still in flight.",
	}

	return rep
}

func accounting(result *driveResult, before, after metricsSnapshot, cfg *config) map[string]any {
	expectedUp := float64(result.counters.sentBytes.Load())
	expectedDown := float64(result.counters.recvBytes.Load())

	actualUp := after.relayUp - before.relayUp
	actualDown := after.relayDown - before.relayDown

	// The echo upstream mirrors upload bytes back, so both directions carry payload.
	if cfg.mode == "echo" {
		expectedDown = expectedUp
		actualDown = after.relayDown - before.relayDown
	}

	return map[string]any{
		"expected_client_to_upstream_bytes": expectedUp,
		"observed_client_to_upstream_bytes": actualUp,
		"delta_client_to_upstream_bytes":    actualUp - expectedUp,
		"expected_upstream_to_client_bytes": expectedDown,
		"observed_upstream_to_client_bytes": actualDown,
		"delta_upstream_to_client_bytes":    actualDown - expectedDown,
		"note": "Observed values are Prometheus counter deltas; a non-zero delta can also come " +
			"from sessions still in flight when the scrape happened.",
	}
}

func latencySummary(samples []time.Duration) map[string]any {
	if len(samples) == 0 {
		return map[string]any{"count": 0}
	}

	percentile := func(p float64) float64 {
		index := int(math.Ceil(p/100*float64(len(samples)))) - 1
		if index < 0 {
			index = 0
		}

		if index >= len(samples) {
			index = len(samples) - 1
		}

		return float64(samples[index].Microseconds()) / 1000
	}

	var sum time.Duration
	for _, sample := range samples {
		sum += sample
	}

	return map[string]any{
		"count": len(samples),
		"mean":  float64(sum.Microseconds()) / float64(len(samples)) / 1000,
		"p50":   percentile(50),
		"p90":   percentile(90),
		"p95":   percentile(95),
		"p99":   percentile(99),
		"max":   float64(samples[len(samples)-1].Microseconds()) / 1000,
	}
}

//nolint:forbidigo // CLI output is the purpose of this command
func printReport(rep *report) {
	fmt.Println("=== SOCKS5 proxy load test ===")
	fmt.Printf("duration: %.1fs  attempted: %d  succeeded: %d  failed: %d\n",
		rep.ElapsedSeconds, rep.Attempted, rep.Succeeded, rep.Failed)
	fmt.Printf("connections/s: %.1f  successful/s: %.1f\n", rep.ConnPerSecond, rep.SuccessPerSec)

	if stages, ok := rep.FailureStages["handshake"]; ok {
		fmt.Printf("failures: handshake=%d connect=%d relay=%d timeout=%d\n",
			stages, rep.FailureStages["connect"], rep.FailureStages["relay"], rep.FailureStages["timeout"])
	}

	if count, ok := rep.SetupLatency["count"].(int); ok && count > 0 {
		fmt.Printf("setup latency ms: mean=%.2f p50=%.2f p90=%.2f p95=%.2f p99=%.2f max=%.2f\n",
			rep.SetupLatency["mean"], rep.SetupLatency["p50"], rep.SetupLatency["p90"],
			rep.SetupLatency["p95"], rep.SetupLatency["p99"], rep.SetupLatency["max"])
	}

	fmt.Printf("payload: sent=%.2f MB/s recv=%.2f MB/s\n",
		rep.Payload["sent_mb_per_s"], rep.Payload["recv_mb_per_s"])

	fmt.Printf("proxy: cpu=%.2fs memory=%.1f MiB goroutines=%.0f\n",
		rep.ProxyMetrics["cpu_seconds_delta"],
		rep.ProxyMetrics["resident_memory_mib_after"],
		rep.ProxyMetrics["goroutines_after"])

	fmt.Printf("accounting delta: up=%.0f down=%.0f bytes\n",
		rep.Accounting["delta_client_to_upstream_bytes"],
		rep.Accounting["delta_upstream_to_client_bytes"])
	fmt.Printf("analytics: dropped=%.0f published=%.0f publish_failures=%.0f\n",
		rep.ProxyMetrics["events_dropped_delta"],
		rep.ProxyMetrics["events_published_delta"],
		rep.ProxyMetrics["publish_failures_delta"])
}

func readMetrics(url string) (metricsSnapshot, error) {
	var snapshot metricsSnapshot

	if url == "" {
		return snapshot, nil
	}

	client := &http.Client{Timeout: 5 * time.Second}

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return snapshot, err
	}

	resp, err := client.Do(request)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return snapshot, err
	}

	values := parsePrometheus(string(body))
	snapshot.cpuSeconds = values["process_cpu_seconds_total"]
	snapshot.residentMem = values["process_resident_memory_bytes"]
	snapshot.goroutines = values["go_goroutines"]
	snapshot.relayUp = values[`socks5_proxy_relay_bytes_total{direction="client_to_upstream"}`]
	snapshot.relayDown = values[`socks5_proxy_relay_bytes_total{direction="upstream_to_client"}`]
	snapshot.accepted = values["socks5_proxy_connections_accepted_total"]
	snapshot.dropped = values[`pipeline_events_dropped_total{stage="collector"}`]
	snapshot.published = values["pipeline_events_published_total"]
	snapshot.publishFail = values["pipeline_publish_failures_total"]

	return snapshot, nil
}

func parsePrometheus(body string) map[string]float64 {
	values := make(map[string]float64)

	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}

		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}

		values[fields[0]] = value
	}

	return values
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}

	return name
}
