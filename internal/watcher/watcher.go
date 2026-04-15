package watcher

import (
	"context"

	homerun "github.com/stuttgart-things/homerun-library/v3"
)

// PitchEvent is a message emitted by a watcher together with an optional
// per-event Redis stream override resolved from the watch config.
// An empty Stream means "use the pitcher's default".
type PitchEvent struct {
	Message homerun.Message
	Stream  string
}

// GitWatcher defines the interface for watching Git repositories for events.
type GitWatcher interface {
	// Watch starts polling the configured repositories and sends events
	// to the returned channel. It blocks until the context is cancelled.
	Watch(ctx context.Context) (<-chan PitchEvent, error)
}
