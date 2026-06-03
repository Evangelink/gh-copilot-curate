// Package cli wires Cobra subcommands to the orchestration in internal/skills.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-copilot-curate/internal/repo"
	"github.com/Evangelink/gh-copilot-curate/internal/skills"
)

// NewRootCmd returns the root cobra command, with all subcommands wired in.
func NewRootCmd(version string) *cobra.Command {
	skills.ToolVersion = version
	cmd := &cobra.Command{
		Use:           "gh-copilot-curate",
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

const rootLong = `gh-copilot-curate installs and updates AI agent skills/plugins from a source
repository (e.g. dotnet/skills) into the current repo.

Skills are installed under .agents/skills/<skill>/ and agents under
.github/agents/<name>.agent.md — the same paths a user would create
manually (and what gh skill --scope=project writes). Tool state
(manifest, lock) is namespaced under .copilot/curate/. AGENTS.md
and .github/instructions/copilot-curate.instructions.md are rewritten with
a managed inventory so the GitHub.com Copilot cloud agent, Copilot CLI,
and IDE Chats pick up installed skills automatically.

Common workflows:
  gh copilot-curate init
  gh copilot-curate add dotnet/skills@v1.0.0
  gh copilot-curate list
  gh copilot-curate update
  gh copilot-curate verify
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

// printLegacyMigrationNotice surfaces the v0.4 → v0.5 one-shot migration
// when a mutating command actually removed the legacy managed block from
// .github/copilot-instructions.md (and optionally the file itself).
// Idempotent by construction: subsequent runs find nothing to migrate and
// pass cleaned=false, so the message only ever prints once per repo.
func printLegacyMigrationNotice(cmd *cobra.Command, cleaned, deleted bool) {
	if !cleaned {
		return
	}
	out := cmd.OutOrStdout()
	if deleted {
		fprintln(out, "migrated v0.4 layout: removed .github/copilot-instructions.md (was empty after the gh-copilot-curate managed block was stripped)")
	} else {
		fprintln(out, "migrated v0.4 layout: stripped the gh-copilot-curate managed block from .github/copilot-instructions.md (preserved your other content)")
	}
	fprintln(out, "  instructions now live in .github/instructions/copilot-curate.instructions.md (path-specific custom instructions, applyTo \"**\")")
}

// printLegacyLayoutMigrationNotice surfaces the v0.5 → v0.6 one-shot move
// from .copilot/plugins/<plugin>/ to .agents/skills/ + .github/agents/.
// Idempotent: only prints when files were actually moved during the call.
func printLegacyLayoutMigrationNotice(cmd *cobra.Command, migrated bool) {
	if !migrated {
		return
	}
	out := cmd.OutOrStdout()
	fprintln(out, "migrated v0.5 install layout: moved skills to .agents/skills/<skill>/ and agents to .github/agents/<name>.agent.md")
	fprintln(out, "  removed .copilot/plugins/ — installed content now lives at the canonical user-equivalent paths (auto-discovered by Copilot CLI, the cloud agent, and gh skill)")
}
