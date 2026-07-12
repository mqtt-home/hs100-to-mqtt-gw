package version

// Version, GitCommit and BuildTime are populated at build time via ldflags.
// See the Makefile / .goreleaser.yml for the stamping recipe.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)
