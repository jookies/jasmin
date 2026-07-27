package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	redis "github.com/redis/go-redis/v9"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
)

// outboundRouteProvisioner adapts the admin RouteProvisioner (opaque JSON
// specs) to the outbound runtime: it parses each spec into an
// outbound.RouteConfig (strict field checking) and rebuilds+swaps the live
// routing table.
type outboundRouteProvisioner struct {
	runtime *outbound.Runtime
}

func (p outboundRouteProvisioner) ApplyRoutes(_ context.Context, routeSpecsJSON []string) error {
	routes := make([]outbound.RouteConfig, 0, len(routeSpecsJSON))
	for index, spec := range routeSpecsJSON {
		var route outbound.RouteConfig
		decoder := json.NewDecoder(bytes.NewReader([]byte(spec)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&route); err != nil {
			return fmt.Errorf("admin route %d: %w", index, err)
		}
		routes = append(routes, route)
	}
	return p.runtime.ApplyAdminRoutes(routes)
}

// outboundUserProvisioner adapts the admin UserProvisioner (opaque JSON) to
// the outbound runtime: it parses each user spec into an outbound.UserConfig
// (strict fields) and installs/removes it in the live billing directory with
// the store-assigned stable uid.
type outboundUserProvisioner struct {
	runtime     *outbound.Runtime
	configUsers int64
}

func (p outboundUserProvisioner) AddUser(username, specJSON string, uid int64) error {
	var user outbound.UserConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(specJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&user); err != nil {
		return fmt.Errorf("admin user %q: %w", username, err)
	}
	if user.Username != username {
		return fmt.Errorf("admin user spec username %q != %q", user.Username, username)
	}
	return p.runtime.AddAdminUser(user, uid)
}

func (p outboundUserProvisioner) RemoveUser(username string) error {
	return p.runtime.RemoveAdminUser(username)
}

func (p outboundUserProvisioner) ConfigUserFloor() int64 { return p.configUsers }

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
