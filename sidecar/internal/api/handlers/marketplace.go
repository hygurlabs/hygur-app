package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/marketplace"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/rs/zerolog"
)

// MarketplaceHandler handles /marketplace/* endpoints.
type MarketplaceHandler struct {
	manager *plugin.Manager
	logger  zerolog.Logger
}

// NewMarketplaceHandler creates a MarketplaceHandler.
func NewMarketplaceHandler(manager *plugin.Manager, logger zerolog.Logger) *MarketplaceHandler {
	return &MarketplaceHandler{
		manager: manager,
		logger:  logger.With().Str("handler", "marketplace").Logger(),
	}
}

// List handles GET /marketplace/connectors.
// Returns BuiltInCatalog with IsInstalled computed from the plugin manager.
func (h *MarketplaceHandler) List(w http.ResponseWriter, r *http.Request) {
	listings := make([]marketplace.ConnectorListing, len(marketplace.BuiltInCatalog))
	copy(listings, marketplace.BuiltInCatalog)
	for i, l := range listings {
		listings[i].IsInstalled = h.manager.HasInstance(l.TypeName)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(listings)
}

// Install handles POST /marketplace/install/{typeID}.
// For built-in connectors, delegates to CreateInstance using typeID as instanceID.
func (h *MarketplaceHandler) Install(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "typeID")

	listing := marketplace.FindByID(typeID)
	if listing == nil {
		h.writeError(w, http.StatusNotFound, "TYPE_NOT_FOUND", "connector type not found in catalog")
		return
	}
	if !listing.IsBuiltIn {
		h.writeError(w, http.StatusUnprocessableEntity, "NOT_BUILT_IN", "only built-in connectors can be installed in v1")
		return
	}
	if h.manager.HasInstance(typeID) {
		// Already installed — return 200 (idempotent)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "already_installed"})
		return
	}
	if !h.manager.HasFactory(typeID) {
		h.writeError(w, http.StatusUnprocessableEntity, "NO_FACTORY", "connector type has no registered factory; registration may be required via /connectors/{type}/instances")
		return
	}
	if err := h.manager.CreateInstance(typeID, typeID, listing.DisplayName, plugin.ConnectorConfig{Enabled: false}); err != nil {
		h.logger.Error().Err(err).Str("type", typeID).Msg("install failed")
		h.writeError(w, http.StatusInternalServerError, "INSTALL_FAILED", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "installed", "id": typeID})
}

func (h *MarketplaceHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
