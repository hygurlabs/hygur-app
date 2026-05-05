package marketplace

// BuiltInCatalog is the static list of connectors shipped with Hygur.
// IsInstalled is computed at request time from the plugin manager.
var BuiltInCatalog = []ConnectorListing{
	{
		ID:          "imap",
		TypeName:    "imap",
		DisplayName: "IMAP Email",
		Description: "Index emails from any IMAP server — Gmail (App Password), Fastmail, Proton Bridge, self-hosted, etc.",
		Version:     "1.0.0",
		Author:      "Hygur Labs",
		IconName:    "envelope",
		IconColor:   "#3B82F6",
		Categories:  []string{"email", "communication"},
		Capabilities: []string{"sync", "index"},
		IsBuiltIn:    true,
		Verified:     true,
		MultiInstance: true,
	},
	{
		ID:          "files",
		TypeName:    "files",
		DisplayName: "Local Files",
		Description: "Watch folders and automatically index documents (PDF, Markdown, Word, plain text, HTML).",
		Version:     "1.0.0",
		Author:      "Hygur Labs",
		IconName:    "folder",
		IconColor:   "#F59E0B",
		Categories:  []string{"files", "productivity"},
		Capabilities: []string{"sync", "index"},
		IsBuiltIn:    true,
		Verified:     true,
		MultiInstance: false,
	},
	{
		ID:          "notes",
		TypeName:    "notes",
		DisplayName: "Notes",
		Description: "Create and index your Hygur notes, making them searchable and available to the AI.",
		Version:     "1.0.0",
		Author:      "Hygur Labs",
		IconName:    "note.text",
		IconColor:   "#F59E0B",
		Categories:  []string{"productivity", "notes"},
		Capabilities: []string{"index", "create"},
		IsBuiltIn:    true,
		Verified:     true,
		MultiInstance: false,
	},
	{
		ID:          "mail",
		TypeName:    "mail",
		DisplayName: "Mail (Gmail / Proton)",
		Description: "Advanced Gmail and Proton Mail integration with thread sync, attachment indexing and label-based filtering.",
		Version:     "1.0.0",
		Author:      "Hygur Labs",
		IconName:    "envelope.badge",
		IconColor:   "#10B981",
		Categories:  []string{"email", "communication"},
		Capabilities: []string{"sync", "index", "search"},
		IsBuiltIn:    true,
		Verified:     true,
		MultiInstance: false,
	},
}

// FindByID returns the listing for typeID, or nil if not found.
func FindByID(typeID string) *ConnectorListing {
	for i := range BuiltInCatalog {
		if BuiltInCatalog[i].TypeName == typeID {
			return &BuiltInCatalog[i]
		}
	}
	return nil
}
