package ingest

// Durable per-item LLM-output cache versions. Bump a version when its prompt or
// logic changes, so cached outputs are recomputed (when their backfill next
// runs) instead of serving a stale, pre-fix result. Each cache stores a
// companion "<key>_version" in the item metadata.
const (
	projectSuggestVersion  = "1"
	mailCategoryVersion    = "3" // v3: classify on the main model (small model gave garbage)
	extractedClaimsVersion = "1"
)

// cachedFresh reports whether m holds key AND its companion "<key>_version"
// equals version. Un-versioned (legacy) entries are treated as stale.
func cachedFresh(m map[string]any, key, version string) bool {
	if m == nil {
		return false
	}
	if _, ok := m[key]; !ok {
		return false
	}
	v, _ := m[key+"_version"].(string)
	return v == version
}
