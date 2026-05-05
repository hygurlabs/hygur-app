// Package marketplace provides the built-in connector catalog and future
// third-party plugin distribution infrastructure.
package marketplace

// ConnectorListing represents a connector entry in the marketplace catalog.
type ConnectorListing struct {
	ID           string   `json:"id"`
	TypeName     string   `json:"type"`
	DisplayName  string   `json:"display_name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	Author       string   `json:"author"`
	IconName     string   `json:"icon_name"`
	IconColor    string   `json:"icon_color"`
	Categories   []string `json:"categories"`
	Capabilities []string `json:"capabilities"`
	IsBuiltIn    bool     `json:"is_built_in"`
	IsInstalled  bool     `json:"is_installed"`
	Verified     bool     `json:"verified"`
	MultiInstance bool    `json:"multi_instance"`
}
