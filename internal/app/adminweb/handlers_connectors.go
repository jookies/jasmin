package adminweb

import (
	"net/http"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
)

// connectorResource is the flat REST shape of one connector: the smppc.Config
// fields inline (their JSON tags are the form field names) plus the Refine
// identity and the desired/observed state. The bind password is write-only —
// it is never sent back to the browser; an empty password on update keeps the
// stored one.
type connectorResource struct {
	ID string `json:"id"`
	smppc.Config
	DesiredStarted bool   `json:"desired_started"`
	Observed       string `json:"observed,omitempty"`
	ManagedBy      string `json:"managed_by"`
}

func toConnectorResource(view admin.ConnectorView) connectorResource {
	cfg := view.Config
	cfg.Password = ""
	return connectorResource{
		ID:             cfg.CID,
		Config:         cfg,
		DesiredStarted: view.DesiredStarted,
		Observed:       view.Observed,
		ManagedBy:      "admin",
	}
}

func (h *Handler) configConnector(cid string) (connectorResource, bool) {
	if h.deps.ConfigConnectors == nil {
		return connectorResource{}, false
	}
	for _, config := range h.deps.ConfigConnectors() {
		if config.CID != cid {
			continue
		}
		config.Password = ""
		resource := connectorResource{ID: cid, Config: config, ManagedBy: "config", Observed: "UNKNOWN"}
		if h.deps.ConnectorStatus != nil {
			if status, err := h.deps.ConnectorStatus(cid); err == nil {
				resource.DesiredStarted = status.Desired
				resource.Observed = string(status.Observed)
			}
		}
		return resource, true
	}
	return connectorResource{}, false
}

func (h *Handler) listConnectors(w http.ResponseWriter, r *http.Request) {
	views, err := h.deps.Connectors.ListConnectors(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
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
	writeList(w, r, resources)
}

func (h *Handler) getConnector(w http.ResponseWriter, r *http.Request) {
	view, err := h.deps.Connectors.GetConnector(r.Context(), r.PathValue("cid"))
	if err != nil {
		if resource, ok := h.configConnector(r.PathValue("cid")); ok {
			writeJSON(w, http.StatusOK, resource)
			return
		}
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toConnectorResource(view))
}

func (h *Handler) createConnector(w http.ResponseWriter, r *http.Request) {
	var res connectorResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.deps.Connectors.CreateConnector(r.Context(), res.Config, res.DesiredStarted); err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := h.deps.Connectors.GetConnector(r.Context(), res.Config.CID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toConnectorResource(view))
}

func (h *Handler) updateConnector(w http.ResponseWriter, r *http.Request) {
	cid := r.PathValue("cid")
	current, err := h.deps.Connectors.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Start from the stored state so fields the form omits keep their values.
	res := connectorResource{Config: current.Config, DesiredStarted: current.DesiredStarted}
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res.Config.CID = cid // the path is authoritative for the identity
	if res.Config.Password == "" {
		res.Config.Password = current.Config.Password
	}
	if err := h.deps.Connectors.UpdateConnector(r.Context(), res.Config); err != nil {
		writeServiceError(w, err)
		return
	}
	if res.DesiredStarted != current.DesiredStarted {
		if err := h.deps.Connectors.SetStarted(r.Context(), cid, res.DesiredStarted); err != nil {
			writeServiceError(w, err)
			return
		}
	}
	view, err := h.deps.Connectors.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toConnectorResource(view))
}

func (h *Handler) deleteConnector(w http.ResponseWriter, r *http.Request) {
	cid := r.PathValue("cid")
	view, err := h.deps.Connectors.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// The service refuses to remove a running connector. The UI's delete is an
	// explicit, confirmed action, so stop it first rather than making the
	// operator perform a two-step dance the API shape does not express.
	if view.DesiredStarted {
		if err := h.deps.Connectors.SetStarted(r.Context(), cid, false); err != nil {
			writeServiceError(w, err)
			return
		}
	}
	if err := h.deps.Connectors.DeleteConnector(r.Context(), cid); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toConnectorResource(view))
}
