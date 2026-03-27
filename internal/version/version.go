// Package version holds build-time metadata injected via -ldflags.
package version

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Info returns a single-line version string for log output and the /version endpoint.
func Info() string {
	return fmt.Sprintf("%s (commit=%s built=%s)", Version, Commit, BuildTime)
}
