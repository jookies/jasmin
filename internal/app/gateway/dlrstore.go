package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	redis "github.com/redis/go-redis/v9"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
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
// store, returning the store, a multipart store over the same client, a
// cleanup that closes the client, and any error. Sharing one Redis endpoint
// keeps the DLR records, the correlation mappings and the reassembly parts in
// one keyspace.
func newDLRRequestStore(redisURL string) (core.DLRRequestStore, smppc.MultipartStore, func(), error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("redis_url: %w", err)
	}
	client := redis.NewClient(options)
	compat := rediscompat.NewClient(client)
	store, err := dlr.NewRequestStore(compat)
	if err != nil {
		_ = client.Close()
		return nil, nil, nil, err
	}
	return store, multipartStore{compat}, func() { _ = client.Close() }, nil
}

// multipartStore adapts the rediscompat client to smppc.MultipartStore: it
// builds the legacy longDeliverSm:<cid>:<ref>:<dst> key and stores/reads/deletes
// segment content there.
type multipartStore struct {
	client *rediscompat.Client
}

func (m multipartStore) StorePart(ctx context.Context, connectorID string, reference uint32, destination string, sequence uint32, content []byte) error {
	key, err := rediscompat.BuildLegacyMultipartKey(connectorID, reference, destination)
	if err != nil {
		return err
	}
	return m.client.WriteLegacyMultipartPart(ctx, key, sequence, content)
}

func (m multipartStore) ReadParts(ctx context.Context, connectorID string, reference uint32, destination string) (map[uint32][]byte, error) {
	key, err := rediscompat.BuildLegacyMultipartKey(connectorID, reference, destination)
	if err != nil {
		return nil, err
	}
	parts, err := m.client.ReadLegacyMultipartParts(ctx, key)
	if errors.Is(err, rediscompat.ErrKeyNotFound) {
		return map[uint32][]byte{}, nil
	}
	return parts, err
}

func (m multipartStore) DeleteParts(ctx context.Context, connectorID string, reference uint32, destination string) error {
	key, err := rediscompat.BuildLegacyMultipartKey(connectorID, reference, destination)
	if err != nil {
		return err
	}
	return m.client.Delete(ctx, key)
}
