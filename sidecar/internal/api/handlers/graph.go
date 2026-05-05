package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// GraphHandler handles graph visualization endpoints.
type GraphHandler struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewGraphHandler creates a new GraphHandler.
func NewGraphHandler(store *store.DB, logger zerolog.Logger) *GraphHandler {
	return &GraphHandler{
		store:  store,
		logger: logger.With().Str("handler", "graph").Logger(),
	}
}

// GraphNode represents a node in the knowledge graph.
type GraphNode struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Label      string         `json:"label"`
	Color      string         `json:"color,omitempty"`
	SourceType string         `json:"source_type,omitempty"`
	SourcePath string         `json:"source_path,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  string         `json:"created_at,omitempty"`
	UpdatedAt  string         `json:"updated_at,omitempty"`
}

// GraphEdge represents an edge between two nodes.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// GraphResponse contains the full graph data.
type GraphResponse struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// Get handles GET /graph - Returns the knowledge graph data.
func (h *GraphHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var nodes []GraphNode
	var edges []GraphEdge

	// Fetch all tags
	tags, err := h.store.ListTags(ctx)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list tags")
		writeGraphError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to fetch tags")
		return
	}

	for _, tag := range tags {
		nodes = append(nodes, GraphNode{
			ID:        "tag:" + tag.ID,
			Type:      "tag",
			Label:     tag.Name,
			Color:     tag.Color,
			CreatedAt: tag.CreatedAt.Format(time.RFC3339),
			UpdatedAt: tag.UpdatedAt.Format(time.RFC3339),
		})
	}

	// Fetch all knowledge items (high limit to get all)
	items, err := h.store.ListKnowledgeItems(ctx, 10000, 0)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list knowledge items")
		writeGraphError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to fetch items")
		return
	}

	for _, item := range items {
		node := GraphNode{
			ID:         "item:" + item.ContentID,
			Type:       "item",
			Label:      item.Title,
			SourceType: item.SourceType,
			Metadata:   item.Metadata,
			CreatedAt:  item.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  item.UpdatedAt.Format(time.RFC3339),
		}
		if item.SourcePath != nil {
			node.SourcePath = *item.SourcePath
		}
		nodes = append(nodes, node)
	}

	// Fetch all projects
	projects, err := h.store.ListProjects(ctx)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list projects")
		writeGraphError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to fetch projects")
		return
	}

	for _, project := range projects {
		nodes = append(nodes, GraphNode{
			ID:        "project:" + project.ProjectID,
			Type:      "project",
			Label:     project.Name,
			CreatedAt: project.CreatedAt.Format(time.RFC3339),
			UpdatedAt: project.UpdatedAt.Format(time.RFC3339),
		})
	}

	// Fetch tag-item relationships
	tagItemLinks, err := h.store.GetAllTagItemLinks(ctx)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to get tag-item links")
		writeGraphError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to fetch tag links")
		return
	}

	for _, link := range tagItemLinks {
		edges = append(edges, GraphEdge{
			Source: "tag:" + link.TagID,
			Target: "item:" + link.ContentID,
			Type:   "tagged",
		})
	}

	// Fetch project-item relationships
	projectItemEdges, err := h.store.GetAllProjectLinks(ctx)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to get project links")
		writeGraphError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to fetch project links")
		return
	}

	for _, link := range projectItemEdges {
		edges = append(edges, GraphEdge{
			Source: "project:" + link.ProjectID,
			Target: "item:" + link.ContentID,
			Type:   "contains",
		})
	}

	// Ensure empty arrays instead of null
	if nodes == nil {
		nodes = []GraphNode{}
	}
	if edges == nil {
		edges = []GraphEdge{}
	}

	writeGraphJSON(w, http.StatusOK, GraphResponse{
		Nodes: nodes,
		Edges: edges,
	})
}

func writeGraphJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeGraphError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}
