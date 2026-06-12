package handlers

import "testing"

// TestTaintedContextFiltering: once the context is tainted, the side-effecting
// tools are removed from the model's toolset while read-only tools remain.
func TestTaintedContextFiltering(t *testing.T) {
	def := func(name string) map[string]any {
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	base := []map[string]any{
		def("search_knowledge_base"), def("web_search"), def("fetch_url"),
		def("create_note"), def("create_calendar_event"),
	}

	out := base
	for _, n := range untrustedDisabledTools {
		out = filterToolDef(out, n)
	}
	names := map[string]bool{}
	for _, d := range out {
		names[d["function"].(map[string]any)["name"].(string)] = true
	}
	for _, gone := range []string{"create_note", "create_calendar_event"} {
		if names[gone] {
			t.Errorf("%s should be dropped once tainted", gone)
		}
	}
	for _, kept := range []string{"search_knowledge_base", "web_search", "fetch_url"} {
		if !names[kept] {
			t.Errorf("%s should remain available", kept)
		}
	}

	if !isUntrustedSourceTool("fetch_url") || !isUntrustedSourceTool("web_search") {
		t.Error("web tools should be flagged as untrusted sources")
	}
	if isUntrustedSourceTool("create_note") || isUntrustedSourceTool("search_knowledge_base") {
		t.Error("non-web tools must not taint the context")
	}
}
