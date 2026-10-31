package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
)

// APIClient consumes the analytics REST API.
type APIClient struct {
	BaseURL string
	Client  *http.Client
}

// NewAPIClient creates a client.
func NewAPIClient(baseURL string, client *http.Client) *APIClient {
	return &APIClient{BaseURL: strings.TrimRight(baseURL, "/"), Client: client}
}

func (c *APIClient) request(ctx context.Context, path string, query url.Values, dst interface{}) error {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if dst != nil {
		if err := json.Unmarshal(body, dst); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Livez checks process liveness.
func (c *APIClient) Livez(ctx context.Context) error {
	return c.request(ctx, "/livez", nil, nil)
}

// Health checks database reachability.
func (c *APIClient) Health(ctx context.Context) error {
	return c.request(ctx, "/health", nil, nil)
}

// TrafficStats fetches aggregate traffic for a time range.
func (c *APIClient) TrafficStats(ctx context.Context, start, end time.Time) (*models.TrafficStats, error) {
	q := url.Values{}
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("end", end.UTC().Format(time.RFC3339))
	var out models.TrafficStats
	if err := c.request(ctx, "/stats/traffic", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TrafficLogs fetches paginated logs for a time range.
func (c *APIClient) TrafficLogs(ctx context.Context, limit, offset int, start, end time.Time) ([]models.TrafficLog, error) {
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("end", end.UTC().Format(time.RFC3339))
	var out []models.TrafficLog
	if err := c.request(ctx, "/logs/traffic", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// TopDestinations fetches top destinations.
func (c *APIClient) TopDestinations(ctx context.Context, limit int) ([]models.DestinationStats, error) {
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	var out []models.DestinationStats
	if err := c.request(ctx, "/stats/top-destinations", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// TopClientIPs fetches top client IPs.
func (c *APIClient) TopClientIPs(ctx context.Context, limit int) ([]models.ClientIPStats, error) {
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	var out []models.ClientIPStats
	if err := c.request(ctx, "/stats/top-client-ips", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}
