package adminweb

import (
	"context"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/modispatch"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// Collectors are the one place each entity's admin-owned rows are merged with
// its config-owned counterparts.
//
// Every list handler used to inline that merge, which was fine while each page
// answered for itself. The topology map needs all of them at once, and a second
// copy of the merge is a second thing to keep in step: the map would eventually
// claim a connector the connectors page did not list, or miss one it did. The
// list handlers and the map now read through the same functions, so the two
// surfaces cannot disagree about what exists.
//
// collectUsers and groupResources already existed for the billing views and
// live in handlers_billing.go and handlers_groups.go; the rest are here.

func (h *Handler) collectConnectors(ctx context.Context) ([]connectorResource, error) {
	views, err := h.deps.Connectors.ListConnectors(ctx)
	if err != nil {
		return nil, err
	}
	configConnectors := []smppc.Config{}
	if h.deps.ConfigConnectors != nil {
		configConnectors = h.deps.ConfigConnectors()
	}
	resources := make([]connectorResource, 0, len(configConnectors)+len(views))
	for _, config := range configConnectors {
		if resource, ok := h.configConnector(config.CID); ok {
			resources = append(resources, resource)
		}
	}
	for _, view := range views {
		resources = append(resources, toConnectorResource(view))
	}
	return resources, nil
}

func (h *Handler) collectRoutes(ctx context.Context) ([]routeResource, error) {
	stored, err := h.deps.Routes.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	configRoutes := []outbound.RouteConfig{}
	if h.deps.ConfigRoutes != nil {
		configRoutes = h.deps.ConfigRoutes()
	}
	resources := make([]routeResource, 0, len(configRoutes)+len(stored))
	for _, route := range configRoutes {
		resources = append(resources, routeFromConfig(route))
	}
	for _, route := range stored {
		resource, convertErr := toRouteResource(route)
		if convertErr != nil {
			return nil, convertErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (h *Handler) collectMORoutes(ctx context.Context) ([]moRouteResource, error) {
	stored, err := h.deps.MORoutes.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	configRoutes := []modispatch.RouteConfig{}
	if h.deps.ConfigMORoutes != nil {
		configRoutes = h.deps.ConfigMORoutes()
	}
	resources := make([]moRouteResource, 0, len(configRoutes)+len(stored))
	for _, route := range configRoutes {
		resources = append(resources, moRouteFromConfig(route))
	}
	for _, route := range stored {
		resource, convertErr := toMORouteResource(route)
		if convertErr != nil {
			return nil, convertErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (h *Handler) collectSMPPsUsers(ctx context.Context) ([]smppsUserResource, error) {
	stored, err := h.deps.SMPPsUsers.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	configUsers := []smppsserver.UserConfig{}
	if h.deps.ConfigSMPPsUsers != nil {
		configUsers = h.deps.ConfigSMPPsUsers()
	}
	resources := make([]smppsUserResource, 0, len(configUsers)+len(stored))
	for _, user := range configUsers {
		if resource, ok := h.configSMPPsUser(user.SystemID); ok {
			resources = append(resources, resource)
		}
	}
	for _, user := range stored {
		resource, convertErr := toSMPPsUserResource(user)
		if convertErr != nil {
			return nil, convertErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

// collectTerminationConnectors answers an empty slice, not an error, when this
// deployment runs no termination manager. The REST handler still answers 404
// for that case; the map simply draws no termination column.
func (h *Handler) collectTerminationConnectors(ctx context.Context) ([]terminationConnectorResource, error) {
	if h.deps.TerminationConnectors == nil {
		return nil, nil
	}
	views, err := h.deps.TerminationConnectors.ListConnectors(ctx)
	if err != nil {
		return nil, err
	}
	configConnectors := []termination.ConnectorConfig{}
	if h.deps.ConfigTerminationConnectors != nil {
		configConnectors = h.deps.ConfigTerminationConnectors()
	}
	resources := make([]terminationConnectorResource, 0, len(configConnectors)+len(views))
	for _, config := range configConnectors {
		if resource, ok := h.configTerminationConnector(config.CID); ok {
			resources = append(resources, resource)
		}
	}
	for _, view := range views {
		resources = append(resources, toTerminationConnectorResource(view))
	}
	return resources, nil
}

// collectMessageConsumers returns the pull credentials with their read
// activity. Nil service means this deployment spools nothing, which is not an
// error — the map simply draws no pull side.
//
// The activity window is deliberately the whole retained audit history (zero
// `since`), matching listMessageConsumers, so the numbers on the map and the
// numbers on the Read Tokens page are the same numbers.
func (h *Handler) collectMessageConsumers(ctx context.Context) ([]messageConsumerResource, error) {
	if h.deps.MessageConsumers == nil {
		return nil, nil
	}
	views, err := h.deps.MessageConsumers.ListConsumersWithActivity(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	resources := make([]messageConsumerResource, 0, len(views))
	for _, view := range views {
		resources = append(resources, toMessageConsumerResource(view))
	}
	return resources, nil
}

// collectPullWindows runs one activity aggregate per analytics window.
//
// It is separate from collectMessageConsumers, and only called when the caller
// asks for analytics, for the reason the service itself documents: each call
// costs an aggregate over the audit table, and the map does not need them.
func (h *Handler) collectPullWindows(ctx context.Context, now time.Time) ([]PullWindowActivity, error) {
	if h.deps.MessageConsumers == nil {
		return nil, nil
	}
	windows := make([]PullWindowActivity, 0, len(AnalyticsWindows))
	for _, window := range AnalyticsWindows {
		views, err := h.deps.MessageConsumers.ListConsumersWithActivity(ctx, now.Add(-window.Span))
		if err != nil {
			return nil, err
		}
		byToken := make(map[string]ActivityWindow, len(views))
		for _, view := range views {
			byToken[view.Consumer.ID] = ActivityWindow{
				Window: window.Name,
				Reads:  view.Activity.Reads,
				Rows:   view.Activity.Rows,
				Denied: view.Activity.Denied,
			}
		}
		windows = append(windows, PullWindowActivity{Window: window.Name, ByToken: byToken})
	}
	return windows, nil
}

func (h *Handler) collectFilters(ctx context.Context) ([]filterResource, error) {
	stored, err := h.deps.Filters.List(ctx)
	if err != nil {
		return nil, err
	}
	resources := make([]filterResource, 0, len(stored))
	for _, entry := range stored {
		resource, convertErr := toFilterResource(entry)
		if convertErr != nil {
			return nil, convertErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (h *Handler) collectHTTPConnectors(ctx context.Context) ([]httpConnectorResource, error) {
	stored, err := h.deps.HTTPConnectors.List(ctx)
	if err != nil {
		return nil, err
	}
	resources := make([]httpConnectorResource, 0, len(stored))
	for _, entry := range stored {
		resource, convertErr := toHTTPConnectorResource(entry)
		if convertErr != nil {
			return nil, convertErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

// collectInterceptors returns both directions in one slice. Nil service means
// interceptor editing was never enabled, which is not an error — the map omits
// the nodes exactly as the console omits the section.
func (h *Handler) collectInterceptors(ctx context.Context) ([]interceptorResource, error) {
	if h.deps.Interceptors == nil {
		return nil, nil
	}
	resources := make([]interceptorResource, 0)
	for _, direction := range []admin.InterceptorDirection{admin.InterceptMT, admin.InterceptMO} {
		stored, err := h.deps.Interceptors.ListInterceptors(ctx, direction)
		if err != nil {
			return nil, err
		}
		for _, entry := range stored {
			resource, convertErr := toInterceptorResource(direction, entry)
			if convertErr != nil {
				return nil, convertErr
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}
