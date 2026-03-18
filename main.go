package main

import (
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"context"
	"net/http"
	"time"


	"github.com/stuttgart-things/homerun2-git-pitcher/internal/banner"
	"github.com/stuttgart-things/homerun2-git-pitcher/internal/config"
	"github.com/stuttgart-things/homerun2-git-pitcher/internal/handlers"
	"github.com/stuttgart-things/homerun2-git-pitcher/internal/middleware"
	"github.com/stuttgart-things/homerun2-git-pitcher/internal/pitcher"
	"github.com/stuttgart-things/homerun2-git-pitcher/internal/watcher"

	homerun "github.com/stuttgart-things/homerun-library/v3"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	banner.Show()
	config.SetupLogging()

	slog.Info("starting homerun2-git-pitcher",
		"version", version,
		"commit", commit,
		"date", date,
		"go", runtime.Version(),
	)


	port := homerun.GetEnv("PORT", "8080")
	mode := homerun.GetEnv("PITCHER_MODE", "redis")

	var p pitcher.Pitcher
	switch mode {
	case "file":
		filePath := homerun.GetEnv("PITCHER_FILE", "pitched.log")
		p = &pitcher.FilePitcher{Path: filePath}
		slog.Info("pitcher mode: file", "path", filePath)
	default:
		redisConfig := config.LoadRedisConfig()
		rp := &pitcher.RedisPitcher{Config: redisConfig}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := rp.HealthCheck(ctx); err != nil {
			slog.Error("redis health check failed", "error", err)
			cancel()
			os.Exit(1)
		}
		cancel()
		p = rp
		slog.Info("pitcher mode: redis", "addr", redisConfig.Addr, "port", redisConfig.Port, "stream", redisConfig.Stream)
	}

	authMiddleware := middleware.TokenAuthMiddleware
	buildInfo := handlers.BuildInfo{Version: version, Commit: commit, Date: date}

	// Rate limit provider is set when the watcher is configured.
	var rateLimitProvider handlers.RateLimitProvider

	// Start GitHub watcher if WATCH_CONFIG is set.
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()

	if watchConfigPath := homerun.GetEnv("WATCH_CONFIG", ""); watchConfigPath != "" {
		watchCfg, err := watcher.LoadWatchConfig(watchConfigPath)
		if err != nil {
			slog.Error("failed to load watch config", "path", watchConfigPath, "error", err)
			os.Exit(1)
		}

		dedupPath := homerun.GetEnv("DEDUP_STATE_FILE", "")
		dedup, err := watcher.NewMemoryDedupStore(watcher.DefaultDedupConfig(), dedupPath)
		if err != nil {
			slog.Error("failed to create dedup store", "error", err)
			os.Exit(1)
		}

		ghWatcher := watcher.NewGitHubWatcher(watchCfg, dedup)

		// Wire rate limit to health endpoint.
		rateLimitProvider = func() handlers.RateLimitInfo {
			s := ghWatcher.RateLimit.Status()
			return handlers.RateLimitInfo{
				Limit:      s.Limit,
				Remaining:  s.Remaining,
				Reset:      s.Reset.Format(time.RFC3339),
				BackingOff: s.BackingOff,
			}
		}

		bridge := &watcher.Bridge{
			Watcher: ghWatcher,
			Pitcher: p,
			Dedup:   dedup,
		}

		go func() {
			slog.Info("starting github watcher",
				"repos", len(watchCfg.GitHub.Repos),
				"config", watchConfigPath,
			)
			if err := bridge.Run(watchCtx); err != nil {
				slog.Error("watcher bridge error", "error", err)
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handlers.NewHealthHandler(buildInfo, rateLimitProvider))
	mux.HandleFunc("/pitch", authMiddleware(handlers.NewPitchHandler(p)))

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: middleware.RequestLogging(mux),
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	watchCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}
	slog.Info("server exited gracefully")
}
