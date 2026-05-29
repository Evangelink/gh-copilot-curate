// Package cli wires Cobra subcommands to the orchestration in internal/skills.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-skill-pack/internal/repo"
	"github.com/Evangelink/gh-skill-pack/internal/skills"
)

// NewRootCmd returns the root cobra command, with all subcommands wired in.
func NewRootCmd(version string) *cobra.Command {
	skills.ToolVersion = version
	cmd := &cobra.Command{
		Use:           "gh-skill-pack",
		Short:         "Install and update Copilot/Claude/Cursor skills and agents in a repo",
		Long:          rootLong,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(
		newInitCmd(),
		newAddCmd(),
		newListCmd(),
		newRemoveCmd(),
		newVerifyCmd(),
		newUpdateCmd(),
	)
	return cmd
}

const rootLong = `gh-skill-pack installs and updates AI agent skills/plugins from a source
repository (e.g. dotnet/skills) into the current repo.

Files are committed under .skills/, and AGENTS.md plus
.github/copilot-instructions.md are updated with a managed block so the
GitHub.com Copilot cloud agent and every contributor pick them up
automatically.

Common workflows:
  gh skill-pack init
  gh skill-pack add dotnet/skills@v1.0.0
  gh skill-pack list
  gh skill-pack update
  gh skill-pack verify
`

// rootFlag adds a --root flag for explicit repo-root selection.
func rootFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "root", "", "repo root (default: auto-detect)")
}

// resolveRoot resolves the explicit --root flag or falls back to auto-detect.
// initMode allows the command to proceed even when no marker exists.
func resolveRoot(explicit string, initMode bool) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		return abs, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, found, err := repo.FindRoot(cwd)
	if err != nil && !initMode {
		return "", err
	}
	if !found && initMode {
		return cwd, nil
	}
	return root, nil
}

// fprintln writes a message to the command's stdout, ignoring errors.
func fprintln(w io.Writer, args ...any) { _, _ = fmt.Fprintln(w, args...) }

// errf wraps formatting into an error.
func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }
