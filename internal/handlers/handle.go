// Package handlers provides HTTP handlers for the analytics API.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	jsonErrorKey = "error"

	defaultStatsLimit = 10
	maxStatsLimit     = 1000
	defaultLogsLimit  = 100
	defaultWindow     = 24 * time.Hour
	maxWindow         = 90 * 24 * time.Hour
)

// Handler handles HTTP requests for the analytics API.
type Handler struct {
	repo storage.Repository
	log  *zap.Logger
}

// NewHandler creates a new HTTP handler with the given repository and logger.
func NewHandler(repo storage.Repository, log *zap.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

// GetTopDestinations returns destinations by connection count.
func (h *Handler) GetTopDestinations(c *gin.Context) {
	limit, err := parseLimit(c, defaultStatsLimit, maxStatsLimit)
	if err != nil {
		badRequest(c, err)

		return
	}

	destinations, err := h.repo.GetTopDestinations(c.Request.Context(), limit)
	if err != nil {
		h.internalError(c, "failed to get top destinations", err)

		return
	}

	c.JSON(http.StatusOK, destinations)
}

// GetTopClientIPs returns SOCKS5 client IPs by connection count.
func (h *Handler) GetTopClientIPs(c *gin.Context) {
	limit, err := parseLimit(c, defaultStatsLimit, maxStatsLimit)
	if err != nil {
		badRequest(c, err)

		return
	}

	clients, err := h.repo.GetTopClientIPs(c.Request.Context(), limit)
	if err != nil {
		h.internalError(c, "failed to get top client IPs", err)

		return
	}

	c.JSON(http.StatusOK, clients)
}

// GetTrafficStats returns aggregate traffic statistics for a bounded time range.
func (h *Handler) GetTrafficStats(c *gin.Context) {
	startTime, endTime, err := parseTimeRange(c)
	if err != nil {
		badRequest(c, err)

		return
	}

	stats, err := h.repo.GetTrafficStats(c.Request.Context(), startTime, endTime)
	if err != nil {
		h.internalError(c, "failed to get traffic stats", err)

		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetTrafficLogs returns paginated traffic logs for a bounded time range.
func (h *Handler) GetTrafficLogs(c *gin.Context) {
	limit, err := parseLimit(c, defaultLogsLimit, maxStatsLimit)
	if err != nil {
		badRequest(c, err)

		return
	}

	offset, err := parseOffset(c)
	if err != nil {
		badRequest(c, err)

		return
	}

	startTime, endTime, err := parseTimeRange(c)
	if err != nil {
		badRequest(c, err)

		return
	}

	logs, err := h.repo.GetTrafficByTimeRange(c.Request.Context(), startTime, endTime, limit, offset)
	if err != nil {
		h.internalError(c, "failed to get traffic logs", err)

		return
	}

	c.JSON(http.StatusOK, logs)
}

// Health verifies database reachability for health/readiness checks.
func (h *Handler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.repo.Ping(ctx); err != nil {
		h.log.Error("database health check failed", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) internalError(c *gin.Context, message string, err error) {
	h.log.Error(message, zap.Error(err))
	c.JSON(http.StatusInternalServerError, gin.H{jsonErrorKey: "internal error"})
}

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{jsonErrorKey: err.Error()})
}

func parseLimit(c *gin.Context, defaultValue, maximum int) (int, error) {
	raw := c.Query("limit")
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maximum {
		return 0, errors.New("limit must be an integer between 1 and " + strconv.Itoa(maximum))
	}

	return value, nil
}

func parseOffset(c *gin.Context) (int, error) {
	raw := c.Query("offset")
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("offset must be a non-negative integer")
	}

	return value, nil
}

func parseTimeRange(c *gin.Context) (startTime, endTime time.Time, err error) {
	now := time.Now().UTC()
	startTime = now.Add(-defaultWindow)
	endTime = now

	if raw := c.Query("start"); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return time.Time{}, time.Time{}, errors.New("start must be an RFC3339 timestamp")
		}

		startTime = parsed
	}

	if raw := c.Query("end"); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return time.Time{}, time.Time{}, errors.New("end must be an RFC3339 timestamp")
		}

		endTime = parsed
	}

	if endTime.Before(startTime) {
		return time.Time{}, time.Time{}, errors.New("end must not be before start")
	}

	if endTime.Sub(startTime) > maxWindow {
		return time.Time{}, time.Time{}, errors.New("time range must not exceed 90 days")
	}

	return startTime, endTime, nil
}
