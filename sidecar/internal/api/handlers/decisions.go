package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// DecisionHandler serves the user's decisions. A decision is a note-like
// knowledge_item (source_type="decision"): a statement (title), an optional
// Markdown rationale (body), tags and a project like a note, plus decision state
// (status, decided_on, the source ids that ground it) in decision_attrs. Bodies
// are indexed, so decisions are searchable and usable by the assistant like notes.
type DecisionHandler struct {
	store            *store.DB
	scanner          *scheduler.DecisionScanner
	embeddingService *llm.EmbeddingService
	logger           zerolog.Logger
}

// NewDecisionHandler creates a DecisionHandler. scanner may be nil (then the
// manual scan endpoint reports unavailable).
func NewDecisionHandler(store *store.DB, scanner *scheduler.DecisionScanner, logger zerolog.Logger) *DecisionHandler {
	return &DecisionHandler{store: store, scanner: scanner, logger: logger.With().Str("handler", "decisions").Logger()}
}

// SetEmbeddingService wires the embedding service so decision rationales are
// indexed like notes. Optional; without it bodies still persist and stay
// searchable via FTS.
func (h *DecisionHandler) SetEmbeddingService(svc *llm.EmbeddingService) { h.embeddingService = svc }

// DecisionResponse is a decision with its note-like properties (rationale, tags,
// project) hydrated alongside the decision state.
type DecisionResponse struct {
	ID         string        `json:"id"`
	Statement  string        `json:"statement"`
	Rationale  string        `json:"rationale"`
	Status     string        `json:"status"`
	DecidedOn  string        `json:"decided_on,omitempty"`
	SourceRefs []string      `json:"source_refs"`
	ProjectID  *string       `json:"project_id,omitempty"`
	Tags       []TagResponse `json:"tags"`
	CreatedAt  string        `json:"created_at"`
	UpdatedAt  string        `json:"updated_at"`
	// Angle A-2a — self-model: set when this decision updates an earlier one (the same
	// matter decided again with a divergent value). UpdatesStatement is the predecessor's
	// statement, for a "updates your earlier decision: ‹…›" marker. Computed read-side.
	UpdatesDecisionID string `json:"updates_decision_id,omitempty"`
	UpdatesStatement  string `json:"updates_statement,omitempty"`
}

func (h *DecisionHandler) toResponse(ctx context.Context, d *store.Decision) DecisionResponse {
	projectID, err := h.store.GetProjectIDForItem(ctx, d.ID)
	if err != nil {
		h.logger.Warn().Err(err).Str("content_id", d.ID).Msg("get project for decision")
	}
	tags, err := h.store.GetTagsForItem(ctx, d.ID)
	if err != nil {
		tags = []*store.Tag{}
	}
	tagResponses := make([]TagResponse, 0, len(tags))
	for _, tag := range tags {
		tagResponses = append(tagResponses, TagResponse{
			ID: tag.ID, Name: tag.Name, Color: tag.Color, AutoRule: tag.AutoRule,
			IsAuto: tag.IsAuto, UsageCount: tag.ItemCount,
			CreatedAt: tag.CreatedAt.Format(time.RFC3339), UpdatedAt: tag.UpdatedAt.Format(time.RFC3339),
		})
	}
	return DecisionResponse{
		ID: d.ID, Statement: d.Statement, Rationale: d.Rationale, Status: d.Status,
		DecidedOn: d.DecidedOn, SourceRefs: d.SourceRefs, ProjectID: projectID,
		Tags: tagResponses, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

// List handles GET /decisions?project_id=&status=
func (h *DecisionHandler) List(w http.ResponseWriter, r *http.Request) {
	decisions, err := h.store.ListDecisions(r.Context(), r.URL.Query().Get("project_id"), r.URL.Query().Get("status"))
	if err != nil {
		h.logger.Error().Err(err).Msg("list decisions failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list decisions")
		return
	}
	evo := h.decisionEvolution(r.Context()) // A-2a: which decision updates an earlier one
	out := make([]DecisionResponse, 0, len(decisions))
	for _, d := range decisions {
		resp := h.toResponse(r.Context(), d)
		if pred, ok := evo[d.ID]; ok {
			resp.UpdatesDecisionID = pred.id
			resp.UpdatesStatement = pred.statement
		}
		out = append(out, resp)
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"decisions": out})
}

// predecessorRef is the earlier decision a successor updates (id + statement).
type predecessorRef struct {
	id        string
	statement string
}

// decisionEvolution computes, across ALL standing decisions, which one updates an
// earlier one — the same (entity, attribute) decided again with a divergent value
// (Angle A-2a). Read-side and LLM-free: it reads the claims G4 has already cached on
// each decision; a decision without cached claims simply doesn't participate, and the
// markers warm up after the nightly contradiction scan. Returns successor id →
// predecessor. Best-effort: any error yields no markers (the list still renders).
func (h *DecisionHandler) decisionEvolution(ctx context.Context) map[string]predecessorRef {
	decs, err := h.store.ListDecisions(ctx, "", store.DecisionStanding)
	if err != nil || len(decs) < 2 {
		return nil
	}
	items := make([]*store.KnowledgeItem, 0, len(decs))
	decidedAt := make(map[string]string, len(decs))
	statement := make(map[string]string, len(decs))
	for _, d := range decs {
		it, gerr := h.store.GetKnowledgeItem(ctx, d.ID)
		if gerr != nil || it == nil {
			continue
		}
		at := d.DecidedOn
		if at == "" {
			at = d.CreatedAt // fall back so a dateless decision still orders
		}
		items = append(items, it)
		decidedAt[it.ContentID] = at
		statement[d.ID] = d.Statement
	}
	evos := contradict.DetectDecisionEvolution(items, decidedAt)
	if len(evos) == 0 {
		return nil
	}
	out := make(map[string]predecessorRef, len(evos))
	for _, e := range evos {
		out[e.SuccessorID] = predecessorRef{id: e.PredecessorID, statement: statement[e.PredecessorID]}
	}
	return out
}

// Get handles GET /decisions/{id}
func (h *DecisionHandler) Get(w http.ResponseWriter, r *http.Request) {
	d, err := h.store.GetDecision(r.Context(), chi.URLParam(r, "id"))
	if err != nil || d == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "decision not found")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, h.toResponse(r.Context(), d))
}

type createDecisionRequest struct {
	Statement string   `json:"statement"`
	Rationale string   `json:"rationale"`
	DecidedOn string   `json:"decided_on"`
	SourceRef string   `json:"source_ref"` // optional: a knowledge_item id that grounds it
	ProjectID *string  `json:"project_id"`
	TagIDs    []string `json:"tag_ids"`
}

// Create handles POST /decisions — a manually-logged decision (status "standing").
func (h *DecisionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Statement) == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "statement required")
		return
	}

	contentID := "decision:" + uuid.New().String()
	now := time.Now()
	decidedOn := strings.TrimSpace(req.DecidedOn)
	if decidedOn == "" {
		decidedOn = now.UTC().Format(time.RFC3339)
	}
	normalized := ingest.NormalizeText(req.Rationale)
	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     store.SourceTypeDecision,
		Title:          strings.TrimSpace(req.Statement),
		NormalizedText: normalized,
		Metadata:       map[string]any{"created_from": "tool", "canonical_date": decidedOn},
		VersionID:      uuid.New().String(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := h.store.InsertKnowledgeItem(r.Context(), item); err != nil {
		h.logger.Error().Err(err).Msg("create decision failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create decision")
		return
	}
	if strings.TrimSpace(normalized) != "" {
		if _, _, idxErr := ingest.IndexSections(r.Context(), h.store, h.embeddingService, contentID, normalized, ingest.DefaultChunkTokenBudget, now); idxErr != nil {
			h.logger.Warn().Err(idxErr).Str("id", contentID).Msg("index decision rationale; still searchable via FTS")
		}
	}
	var refs []string
	if strings.TrimSpace(req.SourceRef) != "" {
		refs = []string{strings.TrimSpace(req.SourceRef)}
	}
	if err := h.store.UpsertDecisionAttrs(r.Context(), contentID, "standing", decidedOn, refs, ""); err != nil {
		h.logger.Error().Err(err).Msg("write decision attrs failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create decision")
		return
	}
	if req.ProjectID != nil && *req.ProjectID != "" {
		if err := h.store.LinkToProject(r.Context(), contentID, *req.ProjectID); err != nil {
			h.logger.Warn().Err(err).Str("id", contentID).Msg("link decision to project")
		}
	}
	for _, tagID := range req.TagIDs {
		if err := h.store.AddTagToItem(r.Context(), contentID, tagID); err != nil {
			h.logger.Warn().Err(err).Str("id", contentID).Str("tag_id", tagID).Msg("add tag to decision")
		}
	}

	d, _ := h.store.GetDecision(r.Context(), contentID)
	if d == nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load decision")
		return
	}
	writeKnowledgeJSON(w, http.StatusCreated, h.toResponse(r.Context(), d))
}

type patchDecisionRequest struct {
	Statement *string  `json:"statement"`
	Rationale *string  `json:"rationale"`
	Status    *string  `json:"status"` // "proposed" | "standing" | "superseded"
	DecidedOn *string  `json:"decided_on"`
	ProjectID *string  `json:"project_id"`
	TagIDs    []string `json:"tag_ids"`
}

// Patch handles PATCH /decisions/{id} — confirm (proposed→standing), supersede,
// or edit the statement/rationale/project/tags.
func (h *DecisionHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	item, err := h.store.GetKnowledgeItem(r.Context(), id)
	if err != nil || item == nil || item.SourceType != store.SourceTypeDecision {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "decision not found")
		return
	}
	var req patchDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	itemChanged, bodyChanged := false, false
	if req.Statement != nil && strings.TrimSpace(*req.Statement) != "" {
		item.Title = strings.TrimSpace(*req.Statement)
		itemChanged = true
	}
	if req.Rationale != nil {
		item.NormalizedText = ingest.NormalizeText(*req.Rationale)
		itemChanged, bodyChanged = true, true
	}
	if itemChanged {
		item.VersionID = uuid.New().String()
		item.UpdatedAt = time.Now()
		if bodyChanged {
			if _, _, idxErr := ingest.IndexSections(r.Context(), h.store, h.embeddingService, id, item.NormalizedText, ingest.DefaultChunkTokenBudget, time.Now()); idxErr != nil {
				h.logger.Warn().Err(idxErr).Str("id", id).Msg("re-index decision rationale; still searchable via FTS")
			}
		}
		if err := h.store.UpdateKnowledgeItem(r.Context(), item); err != nil {
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update decision")
			return
		}
	}

	if req.Status != nil || req.DecidedOn != nil {
		cur, _ := h.store.GetDecision(r.Context(), id)
		status, decidedOn, refs := "standing", "", []string{}
		if cur != nil {
			status, decidedOn, refs = cur.Status, cur.DecidedOn, cur.SourceRefs
		}
		if req.Status != nil && strings.TrimSpace(*req.Status) != "" {
			status = strings.TrimSpace(*req.Status)
		}
		if req.DecidedOn != nil {
			decidedOn = strings.TrimSpace(*req.DecidedOn)
		}
		// Preserve the dedup key (kept by UpsertDecisionAttrs's ON CONFLICT, which
		// leaves dedup_key untouched), so re-confirmed decisions stay deduped.
		if err := h.store.UpsertDecisionAttrs(r.Context(), id, status, decidedOn, refs, ""); err != nil {
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update decision")
			return
		}
	}

	if req.ProjectID != nil {
		_ = h.store.UnlinkFromProject(r.Context(), id)
		if *req.ProjectID != "" {
			if err := h.store.LinkToProject(r.Context(), id, *req.ProjectID); err != nil {
				h.logger.Warn().Err(err).Str("id", id).Msg("link decision to project")
			}
		}
	}
	if req.TagIDs != nil {
		_ = h.store.RemoveAllTagsFromItem(r.Context(), id)
		for _, tagID := range req.TagIDs {
			if err := h.store.AddTagToItem(r.Context(), id, tagID); err != nil {
				h.logger.Warn().Err(err).Str("id", id).Str("tag_id", tagID).Msg("add tag to decision")
			}
		}
	}

	d, _ := h.store.GetDecision(r.Context(), id)
	if d == nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load decision")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, h.toResponse(r.Context(), d))
}

// Delete handles DELETE /decisions/{id} — removes the knowledge_item
// (decision_attrs, project_links and item_tags cascade with it). Used to dismiss
// a proposed decision or delete a logged one.
func (h *DecisionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = h.store.RemoveAllTagsFromItem(r.Context(), id)
	_ = h.store.UnlinkFromProject(r.Context(), id)
	if err := h.store.DeleteKnowledgeItem(r.Context(), id); err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete decision")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Scan handles POST /decisions/scan — manually trigger a grounded scan of recent
// items for new decisions. Runs in the background (it calls the LLM); the client
// refetches the list as proposals land.
func (h *DecisionHandler) Scan(w http.ResponseWriter, r *http.Request) {
	if h.scanner == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "decision scanning is not configured")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if _, err := h.scanner.Run(ctx, time.Now(), true); err != nil {
			h.logger.Warn().Err(err).Msg("manual decision scan failed")
		}
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}
