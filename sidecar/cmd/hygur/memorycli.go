package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
)

// runMemory dispatches the `hygur memory <subcommand>` operator CLI. Runs in-pod
// via `kubectl exec` (like `hygur usage`), never over the tenant API — the home
// tenant is remote-auth (per-device JWT), so there is no static token to hit the
// HTTP /memory/dedup endpoint with; the reconcile is done here against the DB
// directly (DEK from HYGUR_DB_KEY / keychain).
func runMemory(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hygur memory <dedup> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "dedup":
		runMemoryDedup(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: hygur memory <dedup> [flags]")
		os.Exit(2)
	}
}

// memoryBackupRow is the lightweight, restore-sufficient shape dumped to the
// pre-apply backup (the embedding BLOB is intentionally excluded — it is
// regenerable and would bloat the file).
type memoryBackupRow struct {
	MemoryID   string  `json:"memory_id"`
	Type       string  `json:"type"`
	Content    string  `json:"content"`
	ContextID  string  `json:"context_id,omitempty"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  string  `json:"expires_at,omitempty"`
	Score      float64 `json:"score"`
	Source     string  `json:"source"`
	AcceptedAt string  `json:"accepted_at,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
}

// graphTables are the identifier-graph / engram tables the reconcile must NOT
// touch. We count them before/after so an operator can confirm the graph is
// unaffected (the reconcile only ever DELETEs FROM memories).
var graphTables = []string{"entity_mentions", "entity_identifier_link", "entity_edges", "item_norm"}

// runMemoryDedup handles `hygur memory dedup [--apply]`: the Plan A reconcile.
// Dry-run (default) reports the plan without mutating; --apply writes a row
// backup then deletes exactly the planned set (content-duplicates keeping the
// strongest survivor + typed-identifier assertions deferred to the graph).
// Idempotent. Output is JSON on stdout; redacted samples go to stderr with
// --samples so stdout stays machine-parseable and PII-free.
func runMemoryDedup(args []string) {
	fs := flag.NewFlagSet("memory dedup", flag.ExitOnError)
	apply := fs.Bool("apply", false, "delete the planned rows (default: dry-run, no mutation)")
	dbPath := fs.String("db", "", "DB path (default: <data dir>/hygur.db)")
	backupPath := fs.String("backup", "", "backup file (default: <data dir>/backups/memory-dedup-<ts>.json)")
	samples := fs.Bool("samples", false, "print redacted would-delete samples to stderr")
	_ = fs.Parse(args)

	ctx := context.Background()
	src, key := resolveUsageDB(*dbPath)

	db, err := store.OpenKeyedForCLI(src, key, !*apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory dedup:", err)
		os.Exit(1)
	}
	defer db.Close()

	memories, err := db.ListMemoriesAfter(ctx, time.Time{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory dedup: list:", err)
		os.Exit(1)
	}
	rows := make([]store.Memory, 0, len(memories))
	for _, m := range memories {
		rows = append(rows, *m)
	}
	plan := tools.PlanReconcile(rows)

	graphBefore := countGraphTables(ctx, db)

	out := map[string]any{
		"dry_run":         !*apply,
		"total_before":    len(memories),
		"duplicates":      plan.DuplicateCount(),
		"identifiers":     plan.IdentifierCount(),
		"kept_soft_facts": len(plan.Kept),
		"graph_rows":      graphBefore,
	}

	if *samples {
		for _, d := range plan.Deletions {
			fmt.Fprintf(os.Stderr, "would-delete [%s] %s\n", d.Reason, tools.RedactContent(d.Memory.Content))
		}
	}

	if !*apply {
		out["total_after"] = len(memories) - len(plan.Deletions)
		emitJSON(out)
		return
	}

	// APPLY. Back up first — fail closed if the backup cannot be written.
	bpath := *backupPath
	if bpath == "" {
		dir := filepath.Join(filepath.Dir(src), "backups")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "memory dedup: backup dir:", err)
			os.Exit(1)
		}
		bpath = filepath.Join(dir, fmt.Sprintf("memory-dedup-%s.json", time.Now().Format("20060102-150405.000000000")))
	}
	if err := writeMemoryBackup(bpath, memories); err != nil {
		fmt.Fprintln(os.Stderr, "memory dedup: backup:", err)
		os.Exit(1)
	}

	deleted := 0
	for _, d := range plan.Deletions {
		if err := db.DeleteMemory(ctx, d.Memory.MemoryID); err != nil {
			fmt.Fprintf(os.Stderr, "memory dedup: delete %s: %v (backup at %s)\n", d.Memory.MemoryID, err, bpath)
			os.Exit(1)
		}
		deleted++
	}

	out["deleted"] = deleted
	out["total_after"] = len(memories) - deleted
	out["backup_path"] = bpath
	out["graph_rows_after"] = countGraphTables(ctx, db)
	emitJSON(out)
}

// countGraphTables returns COUNT(*) for each identifier-graph table (0 if the
// table is absent), so an operator can confirm before == after.
func countGraphTables(ctx context.Context, db *store.DB) map[string]int {
	counts := make(map[string]int, len(graphTables))
	for _, tbl := range graphTables {
		var n int
		// Table name is from a fixed allow-list (graphTables), never user input.
		row := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tbl)
		if err := row.Scan(&n); err != nil {
			n = -1 // absent / unreadable — surfaced rather than hidden
		}
		counts[tbl] = n
	}
	return counts
}

func writeMemoryBackup(path string, memories []*store.Memory) error {
	out := make([]memoryBackupRow, 0, len(memories))
	for _, m := range memories {
		row := memoryBackupRow{
			MemoryID:  m.MemoryID,
			Type:      string(m.Type),
			Content:   m.Content,
			ContextID: m.ContextID,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
			Score:     m.Score,
			Source:    string(m.Source),
			SessionID: m.SessionID,
		}
		if m.ExpiresAt != nil {
			row.ExpiresAt = m.ExpiresAt.Format(time.RFC3339)
		}
		if m.AcceptedAt != nil {
			row.AcceptedAt = m.AcceptedAt.Format(time.RFC3339)
		}
		out = append(out, row)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "memory dedup: encode:", err)
		os.Exit(1)
	}
}
