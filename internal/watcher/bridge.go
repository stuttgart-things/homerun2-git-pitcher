package watcher

import (
	"context"
	"log/slog"

	"github.com/stuttgart-things/homerun2-git-pitcher/internal/pitcher"
	homerun "github.com/stuttgart-things/homerun-library/v3"
)

// Bridge connects a GitWatcher to a Pitcher, reading events from the
// watcher channel and pitching them to the configured backend.
type Bridge struct {
	Watcher GitWatcher
	Pitcher pitcher.Pitcher
	Dedup   DedupStore // optional; used for periodic flush
}

// Run starts the watcher and pitches all received events until ctx is cancelled.
// It flushes the dedup store on shutdown.
func (b *Bridge) Run(ctx context.Context) error {
	msgs, err := b.Watcher.Watch(ctx)
	if err != nil {
		return err
	}

	for msg := range msgs {
		b.pitch(msg)
	}

	// Flush dedup state on shutdown.
	if b.Dedup != nil {
		if err := b.Dedup.Flush(); err != nil {
			slog.Error("failed to flush dedup state", "error", err)
		}
	}

	slog.Info("watcher bridge stopped")
	return nil
}

// pitch sends a single message to the pitcher backend and logs the result.
func (b *Bridge) pitch(msg homerun.Message) {
	objectID, streamID, err := b.Pitcher.Pitch(msg)
	if err != nil {
		slog.Error("failed to pitch event",
			"title", msg.Title,
			"error", err,
		)
		return
	}

	slog.Info("event pitched",
		"title", msg.Title,
		"severity", msg.Severity,
		"author", msg.Author,
		"objectID", objectID,
		"streamID", streamID,
		"tags", msg.Tags,
		"url", msg.Url,
	)
}
