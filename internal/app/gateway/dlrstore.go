package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	redis "github.com/redis/go-redis/v9"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/modispatch"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
)

// moRouteProvisioner adapts the admin MORouteProvisioner (opaque JSON specs) to
// the MO dispatch service: it parses each spec into a modispatch.RouteConfig
// (strict field checking) and rebuilds+swaps the live dispatch table. A nil
// service means MO dispatch is not running, which makes admin MO routes
// meaningless rather than silently ignored.
type moRouteProvisioner struct {
	service *modispatch.Service
}

func (p moRouteProvisioner) ApplyMORoutes(ctx context.Context, routeSpecsJSON []string) error {
	if p.service == nil {
		return errors.New("MO dispatch is not running; MO routes cannot be applied")
	}
	routes := make([]modispatch.RouteConfig, 0, len(routeSpecsJSON))
	for index, spec := range routeSpecsJSON {
		var route modispatch.RouteConfig
		decoder := json.NewDecoder(bytes.NewReader([]byte(spec)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&route); err != nil {
			return fmt.Errorf("admin MO route %d: %w", index, err)
		}
		routes = append(routes, route)
	}
	return p.service.ApplyRoutes(ctx, routes)
}

// interceptorProvisioner adapts the admin InterceptorProvisioner to the two
// live interception tables: MT lives on the outbound runtime, MO on the gateway
// runtime. Specs are opaque JSON, parsed strictly here where both packages are
// in scope.
type interceptorProvisioner struct {
	outbound *outbound.Runtime
	gateway  *Runtime
}

func (p interceptorProvisioner) ApplyInterceptors(_ context.Context, direction admin.InterceptorDirection, specsJSON []string) error {
	entries := make([]outbound.InterceptorConfig, 0, len(specsJSON))
	for index, spec := range specsJSON {
		var entry outbound.InterceptorConfig
		decoder := json.NewDecoder(bytes.NewReader([]byte(spec)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&entry); err != nil {
			return fmt.Errorf("admin %s interceptor %d: %w", direction, index, err)
		}
		entries = append(entries, entry)
	}
	switch direction {
	case admin.InterceptMT:
		return p.outbound.ApplyAdminMTInterceptors(entries)
	case admin.InterceptMO:
		return p.gateway.ApplyAdminMOInterceptors(entries)
	default:
		return fmt.Errorf("unknown interceptor direction %q", direction)
	}
}

// smppsUserProvisioner adapts the admin SMPPsUserProvisioner to the live SMPPs
// directory. A nil service means the SMPPs server is not running, which makes
// admin bind users meaningless rather than silently inert.
// The SMPPs server is constructed after the admin plane, so the runtime is
// captured and the service read at apply time rather than at wiring time.
type smppsUserProvisioner struct {
	runtime *Runtime
}

func (p smppsUserProvisioner) ApplySMPPsUsers(_ context.Context, specsJSON []string) error {
	service := p.runtime.smppsServer
	if service == nil || service.Directory() == nil {
		if len(specsJSON) == 0 {
			return nil
		}
		return errors.New("the SMPPs server is not running; bind users cannot be applied")
	}
	users := make([]smppsserver.UserConfig, 0, len(specsJSON))
	for index, spec := range specsJSON {
		var user smppsserver.UserConfig
		decoder := json.NewDecoder(bytes.NewReader([]byte(spec)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&user); err != nil {
			return fmt.Errorf("admin SMPPs user %d: %w", index, err)
		}
		users = append(users, user)
	}
	return service.Directory().ApplyUsers(users)
}

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

func (p outboundUserProvisioner) BeginReplaceUser(username, specJSON string, uid int64) (admin.LiveReplacement, error) {
	var user outbound.UserConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(specJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&user); err != nil {
		return nil, fmt.Errorf("admin user %q: %w", username, err)
	}
	if user.Username != username {
		return nil, fmt.Errorf("admin user spec username %q != %q", user.Username, username)
	}
	return p.runtime.BeginReplaceAdminUser(user, uid)
}

func (p outboundUserProvisioner) RemoveUser(username string) error {
	return p.runtime.RemoveAdminUser(username)
}

func (p outboundUserProvisioner) PruneDeletedQuotas(ctx context.Context) (int64, error) {
	return p.runtime.PruneDurableQuotas(ctx)
}

func (p outboundUserProvisioner) ConfigUserFloor() int64 { return p.configUsers }

// outboundGroupProvisioner adapts the admin GroupProvisioner to the outbound
// runtime, decoding each opaque spec into a strict outbound.GroupConfig.
type outboundGroupProvisioner struct {
	runtime      *outbound.Runtime
	configGroups int64
}

func (p outboundGroupProvisioner) AddGroup(gid, specJSON string, number int64) error {
	var group outbound.GroupConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(specJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&group); err != nil {
		return fmt.Errorf("admin group %q: %w", gid, err)
	}
	if group.GID != gid {
		return fmt.Errorf("admin group spec gid %q != %q", group.GID, gid)
	}
	return p.runtime.AddAdminGroup(group, number)
}

func (p outboundGroupProvisioner) BeginReplaceGroup(gid, specJSON string, number int64) (admin.LiveReplacement, error) {
	var group outbound.GroupConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(specJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&group); err != nil {
		return nil, fmt.Errorf("admin group %q: %w", gid, err)
	}
	if group.GID != gid {
		return nil, fmt.Errorf("admin group spec gid %q != %q", group.GID, gid)
	}
	return p.runtime.BeginReplaceAdminGroup(group, number)
}

func (p outboundGroupProvisioner) RemoveGroup(gid string) error {
	return p.runtime.RemoveAdminGroup(gid)
}

// RemoveUser backs GroupService's cascade delete; it is the same live removal
// the user provisioner performs.
func (p outboundGroupProvisioner) RemoveUser(username string) error {
	return p.runtime.RemoveAdminUser(username)
}

func (p outboundGroupProvisioner) PruneDeletedQuotas(ctx context.Context) (int64, error) {
	return p.runtime.PruneDurableQuotas(ctx)
}

func (p outboundGroupProvisioner) ConfigGroupFloor() int64 { return p.configGroups }

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
