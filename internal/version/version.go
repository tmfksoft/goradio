// Package version holds the build version reported by GetServerInfo and
// baked into release binaries via -ldflags (see scripts/package-release.sh
// and .github/workflows/release.yml, which both use the pushed git tag).
package version

// Version is "dev" for a plain `go build` with no -ldflags override --
// only release binaries (built with -X github.com/tmfksoft/goradio/internal/version.Version=vX.Y.Z)
// report a real version.
var Version = "dev"
