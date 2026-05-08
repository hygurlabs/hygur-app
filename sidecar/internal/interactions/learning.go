package interactions

import (
	"context"
	"fmt"

	"github.com/hygur/sidecar/internal/store"
)

// LearningProgress is the response shape consumed by the macOS status bar
// gauge. Coverage is in [0, 1]; pillars expose the per-axis breakdown so the
// UI can reveal *which* axis is lagging when the user clicks the gauge.
type LearningProgress struct {
	Coverage     float64           `json:"coverage"`
	NextStep     string            `json:"next_step"`
	NextStepHint string            `json:"next_step_hint"`
	Pillars      []LearningPillar  `json:"pillars"`
}

// LearningPillar represents one axis contributing to the coverage score.
// Progress is in [0, 1], Weight sums to 1 across pillars.
type LearningPillar struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Progress float64 `json:"progress"`
	Current  int     `json:"current"`
	Target   int     `json:"target"`
	Weight   float64 `json:"weight"`
}

// Pillar v1 weights (Phase 1):
//   - 35 % memory diversity (4 distinct types: fact, action, preference, …)
//   - 30 % memory volume (15 accepted memories total)
//   - 20 % connector breadth (3 distinct connector domains actively ingesting:
//                             mail / notes / files / …)
//   - 15 % chat engagement (50 chat-sent interactions)
//
// Phase 3 will introduce a "feedback signals" pillar and reweight; phase 4
// adds "contradictions resolved". v1 deliberately uses pillars that already
// have data sources today — the gauge has to mean something on day one.
const (
	pillarKeyMemoryDiversity = "memory_diversity"
	pillarKeyMemoryVolume    = "memory_volume"
	pillarKeyConnectors      = "connector_breadth"
	pillarKeyChatEngagement  = "chat_engagement"

	targetMemoryDiversity = 4
	targetMemoryVolume    = 15
	targetConnectors      = 3
	targetChatMessages    = 50

	weightMemoryDiversity = 0.35
	weightMemoryVolume    = 0.30
	weightConnectors      = 0.20
	weightChatEngagement  = 0.15
)

// LearningCalculator computes the learning-progress payload from store
// counters. Stateless beyond the DB handle.
type LearningCalculator struct {
	db *store.DB
}

// NewLearningCalculator returns a calculator backed by the given store.
func NewLearningCalculator(db *store.DB) *LearningCalculator {
	return &LearningCalculator{db: db}
}

// Compute returns the current learning progress snapshot. Each pillar fails
// soft: a count error is propagated rather than silently zeroed because the
// status bar should reflect a real state, not a guess.
func (c *LearningCalculator) Compute(ctx context.Context) (LearningProgress, error) {
	if c == nil || c.db == nil {
		return LearningProgress{}, fmt.Errorf("learning calculator not initialised")
	}

	diversity, err := c.db.CountDistinctMemoryTypesAccepted(ctx)
	if err != nil {
		return LearningProgress{}, fmt.Errorf("memory diversity: %w", err)
	}
	volume, err := c.db.CountAcceptedMemories(ctx)
	if err != nil {
		return LearningProgress{}, fmt.Errorf("memory volume: %w", err)
	}
	connectors, err := c.db.CountActiveConnectorDomains(ctx)
	if err != nil {
		return LearningProgress{}, fmt.Errorf("connector breadth: %w", err)
	}
	chats, err := c.db.CountInteractionsByKind(ctx, string(KindChatMessageSent))
	if err != nil {
		return LearningProgress{}, fmt.Errorf("chat engagement: %w", err)
	}

	pillars := []LearningPillar{
		{
			Key:      pillarKeyMemoryDiversity,
			Label:    "Memory diversity",
			Progress: progressRatio(diversity, targetMemoryDiversity),
			Current:  diversity,
			Target:   targetMemoryDiversity,
			Weight:   weightMemoryDiversity,
		},
		{
			Key:      pillarKeyMemoryVolume,
			Label:    "Accepted memories",
			Progress: progressRatio(volume, targetMemoryVolume),
			Current:  volume,
			Target:   targetMemoryVolume,
			Weight:   weightMemoryVolume,
		},
		{
			Key:      pillarKeyConnectors,
			Label:    "Synced connectors",
			Progress: progressRatio(connectors, targetConnectors),
			Current:  connectors,
			Target:   targetConnectors,
			Weight:   weightConnectors,
		},
		{
			Key:      pillarKeyChatEngagement,
			Label:    "Chat engagement",
			Progress: progressRatio(chats, targetChatMessages),
			Current:  chats,
			Target:   targetChatMessages,
			Weight:   weightChatEngagement,
		},
	}

	coverage := 0.0
	for _, p := range pillars {
		coverage += p.Progress * p.Weight
	}
	if coverage > 1 {
		coverage = 1
	}

	step, hint := nextStep(pillars)

	return LearningProgress{
		Coverage:     coverage,
		NextStep:     step,
		NextStepHint: hint,
		Pillars:      pillars,
	}, nil
}

// progressRatio clamps current/target to [0, 1].
func progressRatio(current, target int) float64 {
	if target <= 0 {
		return 0
	}
	if current >= target {
		return 1
	}
	return float64(current) / float64(target)
}

// nextStep picks the lowest-progress pillar and returns a short, actionable
// hint. The status bar tooltip and the LearningInsightsView both render
// this — empty gamification hurts more than no gamification.
func nextStep(pillars []LearningPillar) (string, string) {
	var lowest *LearningPillar
	for i := range pillars {
		p := &pillars[i]
		if p.Progress >= 1 {
			continue
		}
		if lowest == nil || p.Progress < lowest.Progress {
			lowest = p
		}
	}
	if lowest == nil {
		return "", "Hygur has hit every milestone — phase 1 baseline reached."
	}
	switch lowest.Key {
	case pillarKeyMemoryDiversity:
		return lowest.Key, "Pin a memory of a new type — preferences, facts, projects, tools."
	case pillarKeyMemoryVolume:
		return lowest.Key, "Accept a few more memories so Hygur can ground future replies."
	case pillarKeyConnectors:
		return lowest.Key, "Connect another source — calendar, mail, or a notes folder."
	case pillarKeyChatEngagement:
		return lowest.Key, "Chat more so Hygur learns the questions you actually ask."
	}
	return lowest.Key, "Keep using Hygur — the gauge will catch up."
}
