package adminweb

import (
	"net/http"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// terminationConnectorResource is the flat REST shape of one termination
// connector: the termination.ConnectorConfig fields inline (their JSON tags are
// the form field names) plus the Refine identity and the desired/observed state.
//
// The delivery secret is never returned in the clear. What the browser receives
// is the redaction marker the service produced, alongside has_secret so the form
// can say "signing configured" without inspecting the marker. Sending the marker
// straight back on a PATCH keeps the stored secret — the service enforces that,
// so a round-trip through this form cannot overwrite the credential with "***".
type terminationConnectorResource struct {
	ID string `json:"id"`
	termination.ConnectorConfig
	DesiredStarted bool   `json:"desired_started"`
	Observed       string `json:"observed,omitempty"`
	ManagedBy      string `json:"managed_by"`
	HasSecret      bool   `json:"has_secret"`
	// ClearDeliverySecret is write-only: it is the explicit request to remove a
	// stored signing secret, which an empty field cannot express.
	ClearDeliverySecret bool `json:"clear_delivery_secret,omitempty"`
}

func toTerminationConnectorResource(view admin.TerminationConnectorView) terminationConnectorResource {
	return terminationConnectorResource{
		ID:              view.Config.CID,
		ConnectorConfig: view.Config,
		DesiredStarted:  view.DesiredStarted,
		Observed:        view.Observed,
		ManagedBy:       "admin",
		HasSecret:       view.HasSecret,
	}
}

// requireTerminationConnectors answers 404 when the gateway runs no termination
// connector manager, so a deployment without one exposes no trace of the
// capability rather than an empty list that reads as "none configured".
func (h *Handler) requireTerminationConnectors(w http.ResponseWriter) bool {
	if h.deps.TerminationConnectors == nil {
		writeError(w, http.StatusNotFound, "termination connectors are not enabled on this gateway")
		return false
	}
	return true
}

// configTerminationConnector renders a config-declared termination connector.
// Config owns it, so it is visible and read-only, exactly like a config SMPP
// connector.
func (h *Handler) configTerminationConnector(cid string) (terminationConnectorResource, bool) {
	if h.deps.ConfigTerminationConnectors == nil {
		return terminationConnectorResource{}, false
	}
	for _, config := range h.deps.ConfigTerminationConnectors() {
		if config.CID != cid {
			continue
		}
		resource := terminationConnectorResource{
			ID:              cid,
			ConnectorConfig: config.Redacted(),
			ManagedBy:       "config",
			Observed:        "UNKNOWN",
			HasSecret:       config.HasSecret(),
		}
		if h.deps.TerminationStatus != nil {
			if status, err := h.deps.TerminationStatus(cid); err == nil {
				resource.DesiredStarted = status.Desired
				resource.Observed = string(status.Observed)
			}
		}
		return resource, true
	}
	return terminationConnectorResource{}, false
}

func (h *Handler) listTerminationConnectors(w http.ResponseWriter, r *http.Request) {
	if !h.requireTerminationConnectors(w) {
		return
	}
	views, err := h.deps.TerminationConnectors.ListConnectors(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
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
	writeList(w, r, resources)
}

func (h *Handler) getTerminationConnector(w http.ResponseWriter, r *http.Request) {
	if !h.requireTerminationConnectors(w) {
		return
	}
	cid := r.PathValue("cid")
	view, err := h.deps.TerminationConnectors.GetConnector(r.Context(), cid)
	if err != nil {
		if resource, ok := h.configTerminationConnector(cid); ok {
			writeJSON(w, http.StatusOK, resource)
			return
		}
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTerminationConnectorResource(view))
}

func (h *Handler) createTerminationConnector(w http.ResponseWriter, r *http.Request) {
	if !h.requireTerminationConnectors(w) {
		return
	}
	var res terminationConnectorResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.deps.TerminationConnectors.CreateConnector(r.Context(), res.ConnectorConfig, res.DesiredStarted); err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := h.deps.TerminationConnectors.GetConnector(r.Context(), res.ConnectorConfig.CID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTerminationConnectorResource(view))
}

func (h *Handler) updateTerminationConnector(w http.ResponseWriter, r *http.Request) {
	if !h.requireTerminationConnectors(w) {
		return
	}
	cid := r.PathValue("cid")
	current, err := h.deps.TerminationConnectors.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Start from the stored (redacted) state so fields the form omits keep their
	// values. The secret in this baseline is the marker, not the credential —
	// the service maps both the marker and an empty value back to the stored
	// secret, so the credential never leaves the service to make this round trip.
	res := terminationConnectorResource{ConnectorConfig: current.Config, DesiredStarted: current.DesiredStarted}
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res.ConnectorConfig.CID = cid // the path is authoritative for the identity
	if err := h.deps.TerminationConnectors.UpdateConnector(r.Context(), res.ConnectorConfig, res.ClearDeliverySecret); err != nil {
		writeServiceError(w, err)
		return
	}
	if res.DesiredStarted != current.DesiredStarted {
		if err := h.deps.TerminationConnectors.SetStarted(r.Context(), cid, res.DesiredStarted); err != nil {
			writeServiceError(w, err)
			return
		}
	}
	view, err := h.deps.TerminationConnectors.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTerminationConnectorResource(view))
}

func (h *Handler) deleteTerminationConnector(w http.ResponseWriter, r *http.Request) {
	if !h.requireTerminationConnectors(w) {
		return
	}
	cid := r.PathValue("cid")
	view, err := h.deps.TerminationConnectors.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// The manager refuses to remove a running consumer, and the UI's delete is
	// an explicit confirmed action, so stop it first rather than making the
	// operator perform a two-step dance the API shape does not express.
	if view.DesiredStarted {
		if err := h.deps.TerminationConnectors.SetStarted(r.Context(), cid, false); err != nil {
			writeServiceError(w, err)
			return
		}
	}
	if err := h.deps.TerminationConnectors.DeleteConnector(r.Context(), cid); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTerminationConnectorResource(view))
}

// startTerminationConnector / stopTerminationConnector are the explicit action
// endpoints. The desired_started field on PATCH does the same thing (matching
// the SMPP connector form); these exist because "start this connector" is a
// verb an operator and a script both reach for, and expressing it as a partial
// update of the whole resource invites sending the rest of the resource too.
func (h *Handler) startTerminationConnector(w http.ResponseWriter, r *http.Request) {
	h.setTerminationStarted(w, r, true)
}

func (h *Handler) stopTerminationConnector(w http.ResponseWriter, r *http.Request) {
	h.setTerminationStarted(w, r, false)
}

func (h *Handler) setTerminationStarted(w http.ResponseWriter, r *http.Request, start bool) {
	if !h.requireTerminationConnectors(w) {
		return
	}
	cid := r.PathValue("cid")
	if err := h.deps.TerminationConnectors.SetStarted(r.Context(), cid, start); err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := h.deps.TerminationConnectors.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTerminationConnectorResource(view))
}
