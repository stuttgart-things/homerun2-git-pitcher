package watcher

import (
	"context"

	homerun "github.com/stuttgart-things/homerun-library/v3"
)

// GitWatcher defines the interface for watching Git repositories for events.
type GitWatcher interface {
	// Watch starts polling the configured repositories and sends events
	// as homerun Messages to the returned channel. It blocks until the
	// context is cancelled.
	Watch(ctx context.Context) (<-chan homerun.Message, error)
}
