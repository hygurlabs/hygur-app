package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/rs/zerolog"
)

// SchedulerHandler handles scheduler-related API endpoints.
type SchedulerHandler struct {
	mailScheduler *scheduler.MailIndexScheduler
	logger        zerolog.Logger
}

// NewSchedulerHandler creates a new SchedulerHandler.
func NewSchedulerHandler(mailScheduler *scheduler.MailIndexScheduler, logger zerolog.Logger) *SchedulerHandler {
	return &SchedulerHandler{
		mailScheduler: mailScheduler,
		logger:        logger.With().Str("handler", "scheduler").Logger(),
	}
}

// SchedulerStatusResponse represents the response for GET /scheduler/status.
type SchedulerStatusResponse struct {
	Mail *scheduler.SchedulerStats `json:"mail,omitempty"`
}

// HandleStatus returns the status of all schedulers.
// GET /scheduler/status
func (h *SchedulerHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := SchedulerStatusResponse{}
	if h.mailScheduler != nil {
		stats := h.mailScheduler.Stats()
		response.Mail = &stats
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleMailStart starts the mail indexing scheduler.
// POST /scheduler/mail/start
func (h *SchedulerHandler) HandleMailStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.mailScheduler == nil {
		http.Error(w, "Mail scheduler not configured", http.StatusServiceUnavailable)
		return
	}

	h.mailScheduler.Start()
	h.logger.Info().Msg("Mail scheduler started via API")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

// HandleMailStop stops the mail indexing scheduler.
// POST /scheduler/mail/stop
func (h *SchedulerHandler) HandleMailStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.mailScheduler == nil {
		http.Error(w, "Mail scheduler not configured", http.StatusServiceUnavailable)
		return
	}

	h.mailScheduler.Stop()
	h.logger.Info().Msg("Mail scheduler stopped via API")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// HandleMailTrigger triggers an immediate mail indexing run.
// POST /scheduler/mail/trigger
func (h *SchedulerHandler) HandleMailTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.mailScheduler == nil {
		http.Error(w, "Mail scheduler not configured", http.StatusServiceUnavailable)
		return
	}

	h.mailScheduler.TriggerNow()
	h.logger.Info().Msg("Mail scheduler triggered via API")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "triggered"})
}
