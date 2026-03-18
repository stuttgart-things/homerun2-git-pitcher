package handlers

import (
	"encoding/json"
	"net/http"
	"time"
)

// BuildInfo holds version metadata injected at build time.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// RateLimitInfo provides rate limit data for the health endpoint.
type RateLimitInfo struct {
	Limit      int    `json:"limit"`
	Remaining  int    `json:"remaining"`
	Reset      string `json:"reset"`
	BackingOff bool   `json:"backingOff"`
}

// RateLimitProvider returns the current rate limit status.
// If nil, rate limit info is omitted from the health response.
type RateLimitProvider func() RateLimitInfo

// NewHealthHandler creates a health endpoint handler.
// rateLimit may be nil if no watcher is configured.
func NewHealthHandler(info BuildInfo, rateLimit RateLimitProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		resp := map[string]any{
			"status":  "healthy",
			"time":    time.Now().Format(time.RFC3339),
			"version": info.Version,
			"commit":  info.Commit,
			"date":    info.Date,
		}

		if rateLimit != nil {
			resp["rateLimit"] = rateLimit()
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
	}
}
