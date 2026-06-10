// Package version holds the build identity of the obsd binary, injected at build
// time via -ldflags (see the justfile `build` recipe).
package version

// These are overridden at build time with -X. Defaults are honest about being a
// local, un-stamped build rather than pretending to a release version.
var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String renders a single-line build identity.
func String() string {
	return Version + " (commit " + Commit + ", built " + Date + ")"
}
