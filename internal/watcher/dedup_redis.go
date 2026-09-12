package watcher

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisOpTimeout bounds each Redis call the dedup store makes.
const redisOpTimeout = 5 * time.Second

// RedisDedupStore keeps the seen set in Redis, so it survives container
// restarts and pod reschedules. The file-backed store sat on an emptyDir and
// was only written on a clean shutdown, so a restarted pitcher started empty.
//
// Each repo is a sorted set of event IDs scored by the event's creation time
// (unix seconds). An in-memory copy answers Seen; Redis is written through on
// every Mark and read once at startup. If Redis is unreachable the store logs
// and keeps deduplicating in memory, which is what the pitcher did before.
type RedisDedupStore struct {
	mem    *MemoryDedupStore
	client redis.Cmdable
	prefix string
}

// NewRedisDedupStore loads the seen sets of repos from Redis. Keys are
// prefix + "owner/name".
func NewRedisDedupStore(ctx context.Context, client redis.Cmdable, prefix string, cfg DedupConfig, repos []string) *RedisDedupStore {
	mem, _ := NewMemoryDedupStore(cfg, "")
	s := &RedisDedupStore{mem: mem, client: client, prefix: prefix}

	cutoff := strconv.FormatInt(mem.now().Add(-mem.Retention()).Unix(), 10)
	for _, repo := range repos {
		opCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
		members, err := client.ZRangeByScoreWithScores(opCtx, s.key(repo), &redis.ZRangeBy{Min: cutoff, Max: "+inf"}).Result()
		cancel()
		if err != nil {
			slog.Warn("dedup state load from redis failed, repo starts empty", "repo", repo, "error", err)
			continue
		}
		for _, m := range members {
			id, ok := m.Member.(string)
			if !ok {
				continue
			}
			mem.Mark(repo, id, time.Unix(int64(m.Score), 0))
		}
		if len(members) > 0 {
			slog.Info("dedup state loaded from redis", "repo", repo, "events", len(members))
		}
	}

	return s
}

func (s *RedisDedupStore) key(repo string) string {
	return s.prefix + repo
}

// Seen returns true if eventID has already been recorded for repo.
func (s *RedisDedupStore) Seen(repo, eventID string) bool {
	return s.mem.Seen(repo, eventID)
}

// Mark records the event in memory and in Redis, trimming the set to the
// retention window and size limit.
func (s *RedisDedupStore) Mark(repo, eventID string, createdAt time.Time) {
	s.mem.Mark(repo, eventID, createdAt)

	key := s.key(repo)
	cutoff := s.mem.now().Add(-s.mem.Retention()).Unix()
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(createdAt.Unix()), Member: eventID})
		// Scores are whole seconds, so "(" keeps an event created exactly at the cutoff.
		pipe.ZRemRangeByScore(ctx, key, "-inf", "("+strconv.FormatInt(cutoff, 10))
		pipe.ZRemRangeByRank(ctx, key, 0, int64(-s.mem.config.MaxEventsPerRepo-1))
		// The key outlives its newest entry by an hour, so a repo that is
		// dropped from the watch config does not leave it behind forever.
		pipe.Expire(ctx, key, s.mem.Retention()+time.Hour)
		return nil
	})
	if err != nil {
		slog.Warn("dedup state write to redis failed, kept in memory", "repo", repo, "error", err)
	}
}

// Flush is a no-op: every Mark is already written to Redis.
func (s *RedisDedupStore) Flush() error {
	return nil
}

// Stats returns the count of tracked event IDs per repo.
func (s *RedisDedupStore) Stats() map[string]int {
	return s.mem.Stats()
}

// Retention returns the configured retention window.
func (s *RedisDedupStore) Retention() time.Duration {
	return s.mem.Retention()
}
