package ingest

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

const (
	projSuggestMaxRunes    = 2500 // how much of the item to send
	projSuggestMaxTokens   = 24   // answer is just a project name or NONE
	projSuggestConcurrency = 4    // parallel LLM classifications during backfill
)

// suggestProjectID asks the LLM which of the user's projects an item belongs to,
// or none. Constrained + grounded: reply must be an exact project name from the
// list or NONE; anything else → no suggestion. Returns the matched project ID, or
// "" for none/uncertain.
func suggestProjectID(ctx context.Context, client *llm.Client, item *store.KnowledgeItem, projects []*store.Project) string {
	if client == nil || item == nil || len(projects) == 0 {
		return ""
	}
	body := strings.TrimSpace(item.Title + "\n" + item.DisplayText())
	if r := []rune(body); len(r) > projSuggestMaxRunes {
		body = string(r[:projSuggestMaxRunes])
	}
	if body == "" {
		return ""
	}

	var names []string
	var sb strings.Builder
	sb.WriteString("Projects:\n")
	for _, p := range projects {
		names = append(names, p.Name)
		sb.WriteString("- ")
		sb.WriteString(p.Name)
		if p.Description != nil && strings.TrimSpace(*p.Description) != "" {
			sb.WriteString(": ")
			sb.WriteString(strings.TrimSpace(*p.Description))
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("\nDocument:\n")
	sb.WriteString(body)

	system := "You file a document under ONE of the user's projects, or NONE if none " +
		"clearly fits.\nAllowed projects: " + strings.Join(names, ", ") + ".\n" +
		"STRICT rules: reply ONLY with the EXACT name of a project from the list, or NONE. " +
		"No other text. When in doubt, reply NONE. Never invent a project."

	resp, err := client.Chat(ctx, llm.ChatRequest{
		Category: "ingest",
		Pass:     "projectsuggest",
		Messages: []llm.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: sb.String()},
		},
		Temperature:        llm.Temp(0),
		TopP:               llm.Temp(1),
		Seed:               llm.SeedOf(42),
		MaxTokens:          projSuggestMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil || resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return ""
	}
	ans := strings.TrimSpace(resp.Choices[0].Message.Content)
	if ans == "" {
		ans = strings.TrimSpace(resp.Choices[0].Message.Reasoning)
	}
	return matchProject(ans, projects)
}

// matchProject maps free LLM output to a project ID, or "" for NONE/unknown.
func matchProject(ans string, projects []*store.Project) string {
	low := strings.ToLower(strings.TrimSpace(ans))
	if low == "" || strings.Contains(low, "none") {
		return ""
	}
	for _, p := range projects {
		if name := strings.ToLower(strings.TrimSpace(p.Name)); name != "" && strings.Contains(low, name) {
			return p.ProjectID
		}
	}
	return ""
}

// activeProjects returns the user's non-archived projects.
func (i *Ingestor) activeProjects(ctx context.Context) []*store.Project {
	all, err := i.store.ListProjects(ctx)
	if err != nil {
		return nil
	}
	var out []*store.Project
	for _, p := range all {
		if !p.Archived {
			out = append(out, p)
		}
	}
	return out
}

// suggestProjectForItem classifies one item and caches the result in metadata as
// suggested_project_id (set even when empty, so it isn't reclassified). Skips
// items already linked to a project or already classified. The DetailPanel shows
// a "suggested project" chip only when the value is non-empty and the item has no
// project yet.
func (i *Ingestor) suggestProjectForItem(ctx context.Context, item *store.KnowledgeItem, projects []*store.Project) {
	if item == nil || len(projects) == 0 {
		return
	}
	if cachedFresh(item.Metadata, "suggested_project_id", projectSuggestVersion) {
		return
	}
	if pid, _ := i.store.GetProjectIDForItem(ctx, item.ContentID); pid != nil && *pid != "" {
		return
	}
	pid := suggestProjectID(ctx, i.tier2Client(), item, projects)
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata["suggested_project_id"] = pid // empty = "classified, no match"
	item.Metadata["suggested_project_id_version"] = projectSuggestVersion
	if err := i.store.UpdateKnowledgeItem(ctx, item); err != nil {
		log.Printf("[ingest] project suggestion update failed for %s: %v", item.ContentID, err)
	}
}

// SuggestProjects backfills project suggestions across mail + notes + events that
// have no project and no cached suggestion. Classification runs up to
// projSuggestConcurrency in parallel (LLM); the metadata writes are serialized.
// Idempotent — safe to run at every boot. Returns items processed.
func (i *Ingestor) SuggestProjects(ctx context.Context) (int, error) {
	if i.store == nil || i.tier2Client() == nil {
		return 0, nil
	}
	projects := i.activeProjects(ctx)
	if len(projects) == 0 {
		return 0, nil // nothing to suggest into
	}

	var items []*store.KnowledgeItem
	for _, src := range store.MailAndSourceTypes(store.SourceTypeNote, store.SourceTypeEvent) {
		const batch = 500
		for offset := 0; ; offset += batch {
			page, err := i.store.ListKnowledgeItemsBySourceType(ctx, src, batch, offset)
			if err != nil {
				return 0, err
			}
			items = append(items, page...)
			if len(page) < batch {
				break
			}
		}
	}

	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		sem       = make(chan struct{}, projSuggestConcurrency)
		processed int
	)
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		// Cheap skips (sequential reads) before spending an LLM call.
		if cachedFresh(it.Metadata, "suggested_project_id", projectSuggestVersion) {
			continue
		}
		if pid, _ := i.store.GetProjectIDForItem(ctx, it.ContentID); pid != nil && *pid != "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *store.KnowledgeItem) {
			defer wg.Done()
			defer func() { <-sem }()
			pid := suggestProjectID(ctx, i.tier2Client(), it, projects) // LLM — parallel
			mu.Lock()
			if it.Metadata == nil {
				it.Metadata = map[string]any{}
			}
			it.Metadata["suggested_project_id"] = pid
			it.Metadata["suggested_project_id_version"] = projectSuggestVersion
			if err := i.store.UpdateKnowledgeItem(ctx, it); err != nil {
				log.Printf("[ingest] project suggestion update failed for %s: %v", it.ContentID, err)
			}
			processed++
			mu.Unlock()
		}(it)
	}
	wg.Wait()
	return processed, nil
}
