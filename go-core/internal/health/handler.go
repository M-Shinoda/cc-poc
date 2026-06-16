package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// Tracker holds the last successful price fetch timestamp.
// It is safe to update from multiple goroutines.
type Tracker struct {
	lastFetchUnix atomic.Int64
	startedAt     time.Time
}

func NewTracker() *Tracker {
	t := &Tracker{startedAt: time.Now()}
	return t
}

// RecordFetch updates the last fetch timestamp to now.
func (t *Tracker) RecordFetch() {
	t.lastFetchUnix.Store(time.Now().Unix())
}

// Handler returns an http.HandlerFunc that serves a simple JSON health check.
func (t *Tracker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lastFetch := time.Unix(t.lastFetchUnix.Load(), 0)
		staleSec := time.Since(lastFetch).Seconds()
		status := "ok"
		if staleSec > 30 {
			status = "stale"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         status,
			"uptime_seconds": time.Since(t.startedAt).Seconds(),
			"last_fetch_ago": staleSec,
		})
	}
}
