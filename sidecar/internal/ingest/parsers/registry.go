// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"fmt"
	"strings"
	"sync"

	"github.com/hygur/sidecar/internal/ingest"
)

// Registry manages parser registration and lookup by file extension.
type Registry struct {
	mu      sync.RWMutex
	parsers map[string]ingest.Parser
}

// NewRegistry creates a new parser registry.
func NewRegistry() *Registry {
	return &Registry{
		parsers: make(map[string]ingest.Parser),
	}
}

// Register adds a parser to the registry for all its supported extensions.
// Returns an error if any extension is already registered.
func (r *Registry) Register(p ingest.Parser) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	extensions := p.SupportedExtensions()
	if len(extensions) == 0 {
		return fmt.Errorf("parser supports no extensions")
	}

	// Check for conflicts first
	for _, ext := range extensions {
		ext = normalizeExtension(ext)
		if _, exists := r.parsers[ext]; exists {
			return fmt.Errorf("extension %q already registered", ext)
		}
	}

	// Register all extensions
	for _, ext := range extensions {
		ext = normalizeExtension(ext)
		r.parsers[ext] = p
	}

	return nil
}

// Get returns the parser for the given extension, or nil if not found.
func (r *Registry) Get(ext string) ingest.Parser {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ext = normalizeExtension(ext)
	return r.parsers[ext]
}

// Extensions returns all registered extensions.
func (r *Registry) Extensions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	exts := make([]string, 0, len(r.parsers))
	for ext := range r.parsers {
		exts = append(exts, ext)
	}
	return exts
}

// normalizeExtension ensures the extension is lowercase and has a leading dot.
func normalizeExtension(ext string) string {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}
