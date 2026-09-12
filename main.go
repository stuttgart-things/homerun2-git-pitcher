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

	"github.com/redis/go-redis/v9"
	homerun "github.com/stuttgart-things/homerun-library/v4"
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
	var redisConfig homerun.RedisConfig
	switch mode {
	case "file":
		filePath := homerun.GetEnv("PITCHER_FILE", "pitched.log")
		p = &pitcher.FilePitcher{Path: filePath}
		slog.Info("pitcher mode: file", "path", filePath)
	default:
		redisConfig = config.LoadRedisConfig()
		rp := &pitcher.RedisPitcher{Config: redisConfig}

		// One 5s health check used to end the process when Redis was still
		// starting, and the pod crashlooped until it answered (#50). Wait with
		// bounded backoff for REDIS_STARTUP_TIMEOUT (default 120s) instead. A
		// SIGINT/SIGTERM ends the wait with exit 0; the signal context is
		// released right after, so the shutdown handling below is unchanged.
		startupTimeout, err := homerun.LoadRedisStartupTimeout()
		if err != nil {
			slog.Error("invalid configuration", "error", err)
			os.Exit(1)
		}
		waitCtx, stopWait := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		err = homerun.WaitForRedisContext(waitCtx, redisConfig, startupTimeout)
		interrupted := waitCtx.Err() != nil
		stopWait()
		if interrupted {
			slog.Info("shutdown requested while waiting for redis")
			os.Exit(0)
		}
		if err != nil {
			slog.Error("redis not reachable", "error", err, "addr", redisConfig.Addr, "port", redisConfig.Port, "startup_timeout", startupTimeout.String())
			os.Exit(1)
		}
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
	var bridgeDone chan struct{}

	if watchConfigPath := homerun.GetEnv("WATCH_CONFIG", ""); watchConfigPath != "" {
		watchCfg, err := watcher.LoadWatchConfig(watchConfigPath)
		if err != nil {
			slog.Error("failed to load watch config", "path", watchConfigPath, "error", err)
			os.Exit(1)
		}

		// In redis mode the seen set lives in Redis next to the stream it
		// protects, so a restart or reschedule does not forget it (#34).
		// DEDUP_STATE_FILE only applies to file mode.
		var dedup watcher.DedupStore
		if mode == "file" {
			dedupPath := homerun.GetEnv("DEDUP_STATE_FILE", "")
			fileDedup, err := watcher.NewMemoryDedupStore(watcher.DefaultDedupConfig(), dedupPath)
			if err != nil {
				slog.Error("failed to create dedup store", "error", err)
				os.Exit(1)
			}
			dedup = fileDedup
		} else {
			dedupClient := redis.NewClient(&redis.Options{
				Addr:     redisConfig.Addr + ":" + redisConfig.Port,
				Password: redisConfig.Password,
			})
			defer func() { _ = dedupClient.Close() }()

			stream := watchCfg.Stream
			if stream == "" {
				stream = redisConfig.Stream
			}
			repos := make([]string, 0, len(watchCfg.GitHub.Repos))
			for _, repo := range watchCfg.GitHub.Repos {
				repos = append(repos, repo.FullName())
			}
			prefix := "homerun2-git-pitcher:seen:" + stream + ":"
			dedup = watcher.NewRedisDedupStore(watchCtx, dedupClient, prefix, watcher.DefaultDedupConfig(), repos)
			slog.Info("dedup state in redis", "keyPrefix", prefix, "retention", dedup.Retention().String())
		}

		ghWatcher, err := watcher.NewGitHubWatcher(watchCfg, dedup)
		if err != nil {
			slog.Error("failed to create github watcher", "error", err)
			os.Exit(1)
		}

		overrides := map[string]string{}
		for _, repo := range watchCfg.GitHub.Repos {
			if repo.Stream != "" {
				overrides[repo.FullName()] = repo.Stream
			}
		}
		slog.Info("stream routing resolved",
			"default", watchCfg.Stream,
			"overrides", overrides,
		)

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

		bridgeDone = make(chan struct{})
		go func() {
			defer close(bridgeDone)
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
	// The bridge flushes the file dedup store when the watcher stops; without
	// this wait main could exit first and the state was never written.
	if bridgeDone != nil {
		select {
		case <-bridgeDone:
		case <-time.After(5 * time.Second):
			slog.Warn("watcher bridge did not stop within 5s")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}
	slog.Info("server exited gracefully")
}
