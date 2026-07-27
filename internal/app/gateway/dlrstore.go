package gateway

import (
	"fmt"

	redis "github.com/redis/go-redis/v9"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
)

// newDLRRequestStore opens a Redis client for the submit-side DLR request
// store, returning the store, a cleanup that closes the client, and any error.
// It shares the DLRLookup Redis endpoint so the record this writes and the
// record the lookup reads live in one keyspace.
func newDLRRequestStore(redisURL string) (core.DLRRequestStore, func(), error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("redis_url: %w", err)
	}
	client := redis.NewClient(options)
	store, err := dlr.NewRequestStore(rediscompat.NewClient(client))
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return store, func() { _ = client.Close() }, nil
}
