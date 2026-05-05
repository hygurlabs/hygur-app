// Package version provides build-time version information.
// Variables are injected via ldflags during build.
package version

var (
	// Version is the semantic version of the application.
	Version = "0.6.0-mail-refactor"

	// Commit is the git commit hash at build time.
	Commit = "unknown"

	// BuildTime is the UTC timestamp of the build.
	BuildTime = "unknown"
)
