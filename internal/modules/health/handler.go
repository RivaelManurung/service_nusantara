// Package health exposes liveness and readiness probes.
//
// The previous service had neither, so an orchestrator could only tell that the
// process was running, not that it could still reach PostgreSQL and Redis.
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/database"
)

// probeTimeout bounds a readiness check so a hung dependency cannot hold the
// probe open until the orchestrator's own timeout fires.
const probeTimeout = 2 * time.Second

type Handler struct {
	db      *gorm.DB
	rdb     *redis.Client
	version string
}

func NewHandler(db *gorm.DB, rdb *redis.Client, version string) *Handler {
	return &Handler{db: db, rdb: rdb, version: version}
}

// Live reports that the process is up. It touches no dependency, so a database
// blip does not cause the container to be restarted.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) error {
	httpx.OK(w, r, "service is alive", map[string]string{"version": h.version})
	return nil
}

// Ready reports whether the service can serve traffic right now.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	healthy := true

	if err := database.Ping(ctx, h.db); err != nil {
		checks["postgres"] = err.Error()
		healthy = false
	}
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		checks["redis"] = err.Error()
		healthy = false
	}

	if !healthy {
		return httpx.Unavailable("service is not ready").WithDetails(checks)
	}

	httpx.OK(w, r, "service is ready", checks)
	return nil
}

// Register mounts the probes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /health/live", httpx.Handler(h.Live))
	mux.Handle("GET /health/ready", httpx.Handler(h.Ready))
}
