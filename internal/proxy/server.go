package proxy

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	socks5 "github.com/armon/go-socks5"
	"go.uber.org/zap"
)

type Server struct {
	cfg       *config.Config
	log       *zap.Logger
	collector *pipeline.Collector
	listener  net.Listener
}

func NewServer(cfg *config.Config, log *zap.Logger, collector *pipeline.Collector) *Server {
	return &Server{
		cfg:       cfg,
		log:       log,
		collector: collector,
	}
}

func (s *Server) Start() error {
	conf := &socks5.Config{
		Resolver: &socks5.DNSResolver{},
	}

	// Add dialer with traffic tracking
	conf.Dial = s.dialWithTracking

	socksServer, err := socks5.New(conf)
	if err != nil {
		return fmt.Errorf("failed to create SOCKS5 server: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Proxy.Address, s.cfg.Proxy.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = listener
	s.log.Info("SOCKS5 server started", zap.String("address", addr))

	// Accept connections in a goroutine
	go func() {
		if err := socksServer.Serve(listener); err != nil && err != net.ErrClosed {
			s.log.Error("SOCKS5 server error", zap.Error(err))
		}
	}()

	return nil
}

func (s *Server) dialWithTracking(ctx context.Context, network, addr string) (net.Conn, error) {
	// Default dialer
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	start := time.Now()
	conn, err := dialer.DialContext(ctx, network, addr)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		s.log.Debug("dial failed", zap.String("addr", addr), zap.Error(err))
		return nil, err
	}

	// Wrap the connection to track traffic
	return &trackedConn{
		Conn:      conn,
		server:    s,
		destAddr:  addr,
		timestamp: start,
		latency:   latency,
	}, nil
}

func (s *Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// trackedConn wraps a net.Conn to track bytes read/written
type trackedConn struct {
	net.Conn
	server    *Server
	destAddr  string
	timestamp time.Time
	latency   int64
	bytesIn   int64
	bytesOut  int64
}

func (tc *trackedConn) Read(p []byte) (n int, err error) {
	n, err = tc.Conn.Read(p)
	tc.bytesIn += int64(n)
	return n, err
}

func (tc *trackedConn) Write(p []byte) (n int, err error) {
	n, err = tc.Conn.Write(p)
	tc.bytesOut += int64(n)
	return n, err
}

func (tc *trackedConn) Close() error {
	// Log the traffic event
	remoteAddr := tc.Conn.RemoteAddr()
	var sourceIP string
	if tcpAddr, ok := remoteAddr.(*net.TCPAddr); ok {
		sourceIP = tcpAddr.IP.String()
	}

	destIP, destPort := parseAddress(tc.destAddr)

	event := pipeline.RawTrafficEvent{
		SourceIP:      sourceIP,
		DestinationIP: destIP,
		Domain:        "", // Could be enhanced with reverse DNS lookup
		Port:          destPort,
		Timestamp:     tc.timestamp,
		LatencyMs:     tc.latency,
		BytesIn:       tc.bytesIn,
		BytesOut:      tc.bytesOut,
		Protocol:      "tcp",
	}

	_ = tc.server.collector.Collect(event)

	return tc.Conn.Close()
}

func parseAddress(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}

	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
