package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type HealthHandler struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

func NewHealthHandler(db *pgxpool.Pool, rdb *redis.Client) *HealthHandler {
	return &HealthHandler{db: db, redis: rdb}
}

type healthResponse struct {
	Status   string          `json:"status"`
	Services serviceStatuses `json:"services"`
	Pool     *poolStats      `json:"pool,omitempty"`
}

type serviceStatuses struct {
	Database string `json:"database"`
	Redis    string `json:"redis"`
}

type poolStats struct {
	TotalConns    int32 `json:"total_conns"`
	IdleConns     int32 `json:"idle_conns"`
	AcquiredConns int32 `json:"acquired_conns"`
	MaxConns      int32 `json:"max_conns"`
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	dbStatus := "ok"
	if h.db == nil {
		dbStatus = "unavailable"
	} else if err := h.db.Ping(ctx); err != nil {
		dbStatus = "unavailable"
	}

	redisStatus := "ok"
	if err := h.redis.Ping(ctx).Err(); err != nil {
		redisStatus = "unavailable"
	}

	overall := "ok"
	if dbStatus != "ok" || redisStatus != "ok" {
		overall = "degraded"
	}

	var ps *poolStats
	if h.db != nil {
		s := h.db.Stat()
		ps = &poolStats{
			TotalConns:    s.TotalConns(),
			IdleConns:     s.IdleConns(),
			AcquiredConns: s.AcquiredConns(),
			MaxConns:      s.MaxConns(),
		}
	}

	resp := healthResponse{
		Status: overall,
		Services: serviceStatuses{
			Database: dbStatus,
			Redis:    redisStatus,
		},
		Pool: ps,
	}

	w.Header().Set("Content-Type", "application/json")
	if overall != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(resp)
}
