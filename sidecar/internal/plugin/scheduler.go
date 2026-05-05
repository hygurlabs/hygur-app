package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

// Scheduler déclenche les syncs périodiques via robfig/cron v3 (MIT).
type Scheduler struct {
	manager *Manager
	cron    *cron.Cron
	entries map[string]cron.EntryID // connectorID → entryID (pour suppression)
	mu      sync.Mutex
	logger  zerolog.Logger
}

// NewScheduler crée un nouveau Scheduler lié au Manager donné.
func NewScheduler(manager *Manager, logger zerolog.Logger) *Scheduler {
	return &Scheduler{
		manager: manager,
		cron:    cron.New(),
		entries: make(map[string]cron.EntryID),
		logger:  logger.With().Str("component", "scheduler").Logger(),
	}
}

// Add enregistre une expression cron pour un connecteur.
// Expressions standard 5-champs (min hour day month weekday) + @every, @daily, etc.
// Retourne une erreur si l'expression est invalide — à valider AVANT de persister.
func (s *Scheduler) Add(connectorID, cronExpr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Supprimer l'entrée précédente si elle existe
	if id, ok := s.entries[connectorID]; ok {
		s.cron.Remove(id)
		delete(s.entries, connectorID)
	}

	entryID, err := s.cron.AddFunc(cronExpr, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := s.manager.TriggerSync(ctx, connectorID, SyncOptions{})
		if err != nil {
			s.logger.Error().Str("connector", connectorID).Err(err).Msg("scheduled sync failed")
			return
		}
		s.logger.Info().
			Str("connector", connectorID).
			Int("indexed", result.Processed).
			Int("errors", result.Failed).
			Msg("scheduled sync completed")
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
	}

	s.entries[connectorID] = entryID
	return nil
}

// Remove supprime le job cron d'un connecteur (appelé par DisableConnector).
// N'erreur pas si le connecteur est absent.
func (s *Scheduler) Remove(connectorID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.entries[connectorID]; ok {
		s.cron.Remove(id)
		delete(s.entries, connectorID)
	}
}

// Start lance le cron en arrière-plan.
// Le cron s'arrête automatiquement quand ctx est annulé ou via Stop().
func (s *Scheduler) Start(ctx context.Context) {
	s.cron.Start()
	go func() {
		<-ctx.Done()
		s.cron.Stop()
	}()
}

// Stop arrête le cron immédiatement (appelé par Manager.Stop()).
func (s *Scheduler) Stop() {
	s.cron.Stop()
}
