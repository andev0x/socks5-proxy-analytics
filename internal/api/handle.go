package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Handler struct {
	repo storage.Repository
	log  *zap.Logger
}

func NewHandler(repo storage.Repository, log *zap.Logger) *Handler {
	return &Handler{
		repo: repo,
		log:  log,
	}
}

func (h *Handler) GetTopDomains(c *gin.Context) {
	limit := 10
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	domains, err := h.repo.GetTopDomains(c.Request.Context(), limit)
	if err != nil {
		h.log.Error("failed to get top domains", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve top domains"})
		return
	}
	c.JSON(http.StatusOK, domains)
}

func (h *Handler) GetTopSourceIPs(c *gin.Context) {
	limit := 10
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	ips, err := h.repo.GetTopSourceIPs(c.Request.Context(), limit)
	if err != nil {
		h.log.Error("failed to get top source IPs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve top source IPs"})
		return
	}
	c.JSON(http.StatusOK, ips)
}

func (h *Handler) GetTrafficStats(c *gin.Context) {
	startStr := c.Query("start")
	endStr := c.Query("end")

	var startTime, endTime time.Time

	if startStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = parsed
		}
	} else {
		startTime = time.Now().Add(-24 * time.Hour)
	}

	if endStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = parsed
		}
	} else {
		endTime = time.Now()
	}

	stats, err := h.repo.GetTrafficStats(c.Request.Context(), startTime, endTime)
	if err != nil {
		h.log.Error("failed to get traffic stats", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve traffic stats"})
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *Handler) GetTrafficLogs(c *gin.Context) {
	limit := 100
	offset := 0
	startStr := c.Query("start")
	endStr := c.Query("end")

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}

	var startTime, endTime time.Time

	if startStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = parsed
		}
	} else {
		startTime = time.Now().Add(-24 * time.Hour)
	}

	if endStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = parsed
		}
	} else {
		endTime = time.Now()
	}

	logs, err := h.repo.GetTrafficByTimeRange(c.Request.Context(), startTime, endTime, limit, offset)
	if err != nil {
		h.log.Error("failed to get traffic logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve traffic logs"})
		return
	}
	c.JSON(http.StatusOK, logs)
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
