package mail

import (
	"strings"

	"github.com/hygur/sidecar/internal/plugin"
)

// isMailboxNotFound reports whether a fetch failed only because the folder
// doesn't exist on this account — common when one folder list spans several
// Mail.app accounts with different folder sets (or stale/offline accounts). Such
// folders are skipped, not counted as errors. (-2700 is Mail.app's AppleEvents
// "mailbox not found" code.)
func isMailboxNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "mailbox not found") || strings.Contains(s, "(-2700)")
}

// splitCSV splits a comma-separated config value into trimmed, non-empty parts.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// This file defines how a provider-pinned MailConnector presents itself as a
// distinct connector type (Proton Mail / Gmail / Mail.app), so each appears
// separately in the marketplace and Connectors UI instead of behind one
// "provider" dropdown. The sync engine (syncer.go) is unchanged — only the
// identity (Info) and the configuration form (ConfigSchema) differ per
// provider. IMAP keeps its own dedicated connector (internal/connectors/imap).

// pinnedProviderInfo returns the marketplace/UI identity for a pinned provider.
func pinnedProviderInfo(provider string) plugin.ConnectorInfo {
	switch provider {
	case "gmail":
		return plugin.ConnectorInfo{
			ID:          "gmail",
			Name:        "Gmail",
			Description: "Gmail via OAuth2 — thread sync, attachment indexing, label filtering.",
			Version:     "1.0.0",
			Icon:        "envelope.badge",
			Color:       "#EA4335",
			Tags:        []string{"email", "gmail", "communication"},
		}
	case "mailapp":
		return plugin.ConnectorInfo{
			ID:          "mailapp",
			Name:        "Mail.app",
			Description: "macOS Mail.app — reads your local accounts on-device via Apple Events. Nothing leaves this Mac.",
			Version:     "1.0.0",
			Icon:        "envelope.fill",
			Color:       "#1B72E8",
			Tags:        []string{"email", "macos", "local"},
		}
	default: // proton
		return plugin.ConnectorInfo{
			ID:          "proton",
			Name:        "Proton Mail",
			Description: "Proton Mail via Proton Bridge (local IMAP) — encrypted mail, indexed on-device.",
			Version:     "1.0.0",
			Icon:        "lock.shield",
			Color:       "#6D4AFF",
			Tags:        []string{"email", "proton", "communication"},
		}
	}
}

// pinnedProviderSchema returns the configuration form for a pinned provider —
// only that provider's fields, with no "provider" selector.
func pinnedProviderSchema(provider string) plugin.ConfigSchema {
	limit := plugin.ConfigField{
		Key:         "limit",
		Type:        plugin.FieldInt,
		Label:       "Max threads",
		Default:     "0",
		Description: "Maximum threads to index per sync (0 = no limit)",
	}
	schedule := plugin.ConfigField{Key: "schedule", Type: plugin.FieldCron, Label: "Schedule"}

	switch provider {
	case "gmail":
		return plugin.ConfigSchema{Groups: []plugin.ConfigGroup{
			{
				Title: "Authentication",
				Fields: []plugin.ConfigField{
					{Key: "gmail_oauth", Type: plugin.FieldOAuth, Label: "Gmail connection", Required: true},
				},
			},
			{
				Title: "Synchronization",
				Fields: []plugin.ConfigField{
					{
						Key:         "gmail_mailbox",
						Type:        plugin.FieldMultiEnum,
						Label:       "Labels to sync",
						Description: "Connect, then pick the Gmail labels to sync (loaded from your account).",
						// No static options: the UI populates them from
						// GET /connectors/{id}/mailboxes once connected.
					},
					limit,
					schedule,
				},
			},
		}}
	case "mailapp":
		return plugin.ConfigSchema{Groups: []plugin.ConfigGroup{
			{
				Title: "Authorization",
				Fields: []plugin.ConfigField{
					{
						Key:         "mailapp_automation",
						Type:        plugin.FieldPermissionCheck,
						Label:       "Automation permission",
						Description: "Hygur needs Automation permission for Mail.app to read your local emails. Nothing leaves this Mac.",
						Default:     "Open System Settings",
					},
				},
			},
			{
				Title: "Synchronization",
				Fields: []plugin.ConfigField{
					{
						Key:         "mailapp_mailbox",
						Type:        plugin.FieldMultiEnum,
						Label:       "Folders to sync",
						Description: "Pick the Mail.app folders to sync (loaded from your accounts). Applies to all accounts.",
						// Populated from GET /connectors/{id}/mailboxes.
					},
					limit,
					schedule,
				},
			},
		}}
	default: // proton
		return plugin.ConfigSchema{Groups: []plugin.ConfigGroup{
			{
				Title: "Proton Bridge authentication",
				Fields: []plugin.ConfigField{
					{Key: "username", Type: plugin.FieldString, Label: "Proton username", Required: true},
					{Key: "password", Type: plugin.FieldSecret, Label: "Bridge password", Required: true},
				},
			},
			{
				Title: "Synchronization",
				Fields: []plugin.ConfigField{
					{
						Key:         "proton_mailbox",
						Type:        plugin.FieldMultiEnum,
						Label:       "Mailboxes to sync",
						Default:     "INBOX",
						Description: "Connect, then pick the mailboxes to sync (loaded from Proton Bridge).",
						// No static options: the UI populates them from
						// GET /connectors/{id}/mailboxes once connected.
					},
					limit,
					schedule,
				},
			},
		}}
	}
}
