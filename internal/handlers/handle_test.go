package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestHealth(t *testing.T) {
	repo := &fakeRepository{}
	router := testRouter(repo)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	repo.pingErr = errors.New("database down")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestTopDestinationsValidationAndDefaults(t *testing.T) {
	repo := &fakeRepository{}
	router := testRouter(repo)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/stats/top-destinations", http.NoBody))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	if repo.lastLimit != defaultStatsLimit {
		t.Errorf("default limit = %d, want %d", repo.lastLimit, defaultStatsLimit)
	}

	for _, raw := range []string{"0", "-1", "1001", "abc"} {
		response = httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/stats/top-destinations?limit="+raw, http.NoBody))

		if response.Code != http.StatusBadRequest {
			t.Errorf("limit=%s status = %d, want 400", raw, response.Code)
		}
	}
}

func TestTrafficLogsValidation(t *testing.T) {
	repo := &fakeRepository{}
	router := testRouter(repo)

	cases := []string{
		"?offset=-1",
		"?start=not-a-time",
		"?end=not-a-time",
		"?start=2026-01-02T00:00:00Z&end=2026-01-01T00:00:00Z",
		"?start=2026-01-01T00:00:00Z&end=2026-06-01T00:00:00Z",
	}

	for _, query := range cases {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/logs/traffic"+query, http.NoBody))

		if response.Code != http.StatusBadRequest {
			t.Errorf("query %s status = %d, want 400", query, response.Code)
		}
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/logs/traffic?limit=50&offset=10", http.NoBody))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	if repo.lastLimit != 50 || repo.lastOffset != 10 {
		t.Errorf("limit/offset = %d/%d, want 50/10", repo.lastLimit, repo.lastOffset)
	}

	if repo.lastEnd.Sub(repo.lastStart) > maxWindow {
		t.Errorf("default window = %v, want <= %v", repo.lastEnd.Sub(repo.lastStart), maxWindow)
	}
}

func TestGetTrafficStatsPassesValidatedRange(t *testing.T) {
	repo := &fakeRepository{}
	router := testRouter(repo)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/stats/traffic?start=2026-01-01T00:00:00Z&end=2026-01-02T00:00:00Z",
		http.NoBody,
	)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	var stats models.TrafficStats
	if err := json.Unmarshal(response.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if stats.TotalConnections != 3 {
		t.Errorf("total connections = %d, want 3", stats.TotalConnections)
	}

	if repo.lastStart.Format(time.RFC3339) != "2026-01-01T00:00:00Z" {
		t.Errorf("start = %v", repo.lastStart)
	}
}

func testRouter(repo *fakeRepository) http.Handler {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	handler := NewHandler(repo, zap.NewNop())

	router.GET("/health", handler.Health)
	router.GET("/stats/top-destinations", handler.GetTopDestinations)
	router.GET("/stats/traffic", handler.GetTrafficStats)
	router.GET("/logs/traffic", handler.GetTrafficLogs)

	return router
}

type fakeRepository struct {
	pingErr    error
	lastLimit  int
	lastOffset int
	lastStart  time.Time
	lastEnd    time.Time
}

func (r *fakeRepository) SaveTrafficLog(context.Context, *models.TrafficLog) error {
	return nil
}

func (r *fakeRepository) SaveTrafficLogs(context.Context, []*models.TrafficLog) error {
	return nil
}

func (r *fakeRepository) GetTopDestinations(
	_ context.Context, limit int,
) ([]models.DestinationStats, error) {
	r.lastLimit = limit

	return []models.DestinationStats{}, nil
}

func (r *fakeRepository) GetTopClientIPs(
	_ context.Context, limit int,
) ([]models.ClientIPStats, error) {
	r.lastLimit = limit

	return []models.ClientIPStats{}, nil
}

func (r *fakeRepository) GetTrafficStats(
	_ context.Context, startTime, endTime time.Time,
) (*models.TrafficStats, error) {
	r.lastStart = startTime
	r.lastEnd = endTime

	return &models.TrafficStats{TotalConnections: 3}, nil
}

func (r *fakeRepository) GetTrafficByTimeRange(
	_ context.Context, startTime, endTime time.Time, limit, offset int,
) ([]models.TrafficLog, error) {
	r.lastStart = startTime
	r.lastEnd = endTime
	r.lastLimit = limit
	r.lastOffset = offset

	return []models.TrafficLog{}, nil
}

func (r *fakeRepository) Ping(context.Context) error {
	return r.pingErr
}

func (r *fakeRepository) Close() error {
	return nil
}
