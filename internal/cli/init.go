package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-copilot-curate/internal/agents"
	"github.com/Evangelink/gh-copilot-curate/internal/manifest"
	"github.com/Evangelink/gh-copilot-curate/internal/skills"
)

func newInitCmd() *cobra.Command {
	var rootDir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create .copilot/ scaffolding and AGENTS.md managed block (non-destructive)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := resolveRoot(rootDir, true)
			if err != nil {
				return err
			}
			if err := skills.CheckNoLegacyLayout(root); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Join(root, manifest.ToolStateDir), 0o755); err != nil {
				return err
			}
			// Non-destructive: only write manifest if absent.
			if _, err := os.Stat(filepath.Join(root, manifest.ManifestPath)); os.IsNotExist(err) {
				m := &manifest.Manifest{Version: manifest.SchemaVersion}
				if err := manifest.SaveManifest(root, m); err != nil {
					return err
				}
				fprintln(cmd.OutOrStdout(), "created", manifest.ManifestPath)
			} else {
				fprintln(cmd.OutOrStdout(), manifest.ManifestPath, "already exists, leaving untouched")
			}
			// Write .copilot/.gitattributes so localHash stays stable across
			// CRLF/LF checkouts. Idempotent.
			if err := skills.EnsurePackGitAttributes(root); err != nil {
				return err
			}
			// Bootstrap AGENTS.md managed block (empty) so contributors see the
			// section header and the cloud agent picks up the marker on first
			// install.
			if _, err := agents.WriteManagedBlock(root, agents.AgentsFile, nil); err != nil {
				return err
			}
			fprintln(cmd.OutOrStdout(), "managed block ready in", agents.AgentsFile)
			// v0.4 → v0.5 one-shot migration: if a previous version left a
			// managed block in .github/copilot-instructions.md, strip it
			// (and delete the file if it becomes empty). We deliberately do
			// NOT bootstrap the new instructions file here — it is created
			// lazily on first `add` so an empty global path-spec doesn't
			// add noise to repos that only ran `init`.
			cleaned, deleted, err := agents.CleanLegacyCopilotInstructionsBlock(root)
			if err != nil {
				return err
			}
			if cleaned {
				if deleted {
					fprintln(cmd.OutOrStdout(), "migrated v0.4 layout: removed", agents.LegacyCopilotInstFile)
				} else {
					fprintln(cmd.OutOrStdout(), "migrated v0.4 layout: stripped managed block from", agents.LegacyCopilotInstFile)
				}
			}
			fprintln(cmd.OutOrStdout(), "\nNext: gh copilot-curate add <owner/repo>[@ref]")
			return nil
		},
	}
	rootFlag(cmd, &rootDir)
	return cmd
}
