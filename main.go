// Command gh-skills is a GitHub CLI extension that installs and updates
// Copilot/Claude/Cursor skills and agents from a source repository into a
// target repository (committed) or the user's home directory.
package main

import (
	"fmt"
	"os"

	"github.com/Evangelink/gh-skills/internal/cli"
)

// version is the build version. Overridden by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := cli.NewRootCmd(buildVersionString()).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func buildVersionString() string {
	if commit == "none" && date == "unknown" {
		return version
	}
	return fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
}
