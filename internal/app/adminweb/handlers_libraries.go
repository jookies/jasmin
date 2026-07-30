package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/pumpitspace/synevyr/internal/app/admin"
)

var (
	filterIDPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)
	httpConnectorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,25}$`)
)

type filterResource struct {
	ID   string            `json:"id"`
	FID  string            `json:"fid"`
	Type string            `json:"type"`
	Args map[string]string `json:"args,omitempty"`
}

type storedFilterSpec struct {
	Type string            `json:"type"`
	Args map[string]string `json:"args,omitempty"`
}

var filterArguments = map[string][]string{
	"TransparentFilter":     {},
	"ConnectorFilter":       {"cid"},
	"UserFilter":            {"uid"},
	"GroupFilter":           {"gid"},
	"SourceAddrFilter":      {"source_addr"},
	"DestinationAddrFilter": {"destination_addr"},
	"ShortMessageFilter":    {"short_message"},
	"DateIntervalFilter":    {"dateInterval"},
	"TimeIntervalFilter":    {"timeInterval"},
	"EvalPyFilter":          {"pyCode"},
	"TagFilter":             {"tag"},
}

func validateFilterResource(resource filterResource) error {
	if !filterIDPattern.MatchString(resource.FID) {
		return fmt.Errorf("filter id must be 1–16 letters, numbers, underscores or dashes")
	}
	required, ok := filterArguments[resource.Type]
	if !ok {
		return fmt.Errorf("unknown filter type %q", resource.Type)
	}
	for _, name := range required {
		if strings.TrimSpace(resource.Args[name]) == "" {
			return fmt.Errorf("%s is required for %s", name, resource.Type)
		}
	}
	return nil
}

func toFilterResource(stored admin.StoredNamedSpec) (filterResource, error) {
	var spec storedFilterSpec
	if err := json.Unmarshal([]byte(stored.SpecJSON), &spec); err != nil {
		return filterResource{}, fmt.Errorf("filter %q: stored spec is not valid JSON: %w", stored.ID, err)
	}
	return filterResource{ID: stored.ID, FID: stored.ID, Type: spec.Type, Args: spec.Args}, nil
}

func (h *Handler) listFilters(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.Filters.List(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resources := make([]filterResource, 0, len(stored))
	for _, entry := range stored {
		resource, err := toFilterResource(entry)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resources = append(resources, resource)
	}
	writeList(w, r, resources)
}

func (h *Handler) getFilter(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.Filters.Get(r.Context(), r.PathValue("fid"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toFilterResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (h *Handler) putFilter(w http.ResponseWriter, r *http.Request, fid string, resource filterResource, status int) {
	resource.FID = fid
	if err := validateFilterResource(resource); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	spec, err := json.Marshal(storedFilterSpec{Type: resource.Type, Args: resource.Args})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Filters.Put(r.Context(), fid, string(spec)); err != nil {
		writeServiceError(w, err)
		return
	}
	stored, err := h.deps.Filters.Get(r.Context(), fid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	result, err := toFilterResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, result)
}

func (h *Handler) createFilter(w http.ResponseWriter, r *http.Request) {
	var resource filterResource
	if err := decodeBody(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putFilter(w, r, resource.FID, resource, http.StatusCreated)
}

func (h *Handler) updateFilter(w http.ResponseWriter, r *http.Request) {
	fid := r.PathValue("fid")
	stored, err := h.deps.Filters.Get(r.Context(), fid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toFilterResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := decodeBody(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putFilter(w, r, fid, resource, http.StatusOK)
}

func (h *Handler) deleteFilter(w http.ResponseWriter, r *http.Request) {
	fid := r.PathValue("fid")
	stored, err := h.deps.Filters.Get(r.Context(), fid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toFilterResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Filters.Delete(r.Context(), fid); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

type httpConnectorResource struct {
	ID      string `json:"id"`
	CID     string `json:"cid"`
	BaseURL string `json:"baseurl"`
	Method  string `json:"method"`
}

func validateHTTPConnector(resource httpConnectorResource) error {
	if !httpConnectorPattern.MatchString(resource.CID) {
		return fmt.Errorf("connector id must be 3–25 letters, numbers, underscores or dashes")
	}
	parsed, err := url.ParseRequestURI(resource.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("base URL must be an absolute HTTP or HTTPS URL")
	}
	method := strings.ToUpper(resource.Method)
	if method != "GET" && method != "POST" {
		return fmt.Errorf("method must be GET or POST")
	}
	return nil
}

func toHTTPConnectorResource(stored admin.StoredNamedSpec) (httpConnectorResource, error) {
	var resource httpConnectorResource
	if err := json.Unmarshal([]byte(stored.SpecJSON), &resource); err != nil {
		return httpConnectorResource{}, fmt.Errorf("HTTP connector %q: stored spec is not valid JSON: %w", stored.ID, err)
	}
	resource.ID, resource.CID = stored.ID, stored.ID
	resource.Method = strings.ToUpper(resource.Method)
	return resource, nil
}

func (h *Handler) listHTTPConnectors(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.HTTPConnectors.List(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resources := make([]httpConnectorResource, 0, len(stored))
	for _, entry := range stored {
		resource, err := toHTTPConnectorResource(entry)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resources = append(resources, resource)
	}
	writeList(w, r, resources)
}

func (h *Handler) getHTTPConnector(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.HTTPConnectors.Get(r.Context(), r.PathValue("cid"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toHTTPConnectorResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (h *Handler) putHTTPConnector(w http.ResponseWriter, r *http.Request, cid string, resource httpConnectorResource, status int) {
	resource.CID = cid
	resource.Method = strings.ToUpper(resource.Method)
	if resource.Method == "" {
		resource.Method = "GET"
	}
	if err := validateHTTPConnector(resource); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	spec, err := json.Marshal(map[string]string{"baseurl": resource.BaseURL, "method": resource.Method})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.HTTPConnectors.Put(r.Context(), cid, string(spec)); err != nil {
		writeServiceError(w, err)
		return
	}
	stored, err := h.deps.HTTPConnectors.Get(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	result, err := toHTTPConnectorResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, result)
}

func (h *Handler) createHTTPConnector(w http.ResponseWriter, r *http.Request) {
	var resource httpConnectorResource
	if err := decodeBody(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putHTTPConnector(w, r, resource.CID, resource, http.StatusCreated)
}

func (h *Handler) updateHTTPConnector(w http.ResponseWriter, r *http.Request) {
	cid := r.PathValue("cid")
	stored, err := h.deps.HTTPConnectors.Get(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toHTTPConnectorResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := decodeBody(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putHTTPConnector(w, r, cid, resource, http.StatusOK)
}

func (h *Handler) deleteHTTPConnector(w http.ResponseWriter, r *http.Request) {
	cid := r.PathValue("cid")
	stored, err := h.deps.HTTPConnectors.Get(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toHTTPConnectorResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.HTTPConnectors.Delete(r.Context(), cid); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}
