package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-skills/internal/agents"
	"github.com/Evangelink/gh-skills/internal/manifest"
	"github.com/Evangelink/gh-skills/internal/skills"
)

func newInitCmd() *cobra.Command {
	var rootDir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create .skills/ scaffolding and AGENTS.md managed block (non-destructive)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := resolveRoot(rootDir, true)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Join(root, manifest.SkillsDir, "plugins"), 0o755); err != nil {
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
			// Write .skills/.gitattributes so localHash stays stable across
			// CRLF/LF checkouts. Idempotent.
			if err := skills.EnsureSkillsGitAttributes(root); err != nil {
				return err
			}
			// Bootstrap AGENTS.md managed block (empty) so contributors see the
			// section header and the cloud agent picks up the marker on first
			// install.
			if _, err := agents.WriteManagedBlock(root, agents.AgentsFile, nil); err != nil {
				return err
			}
			fprintln(cmd.OutOrStdout(), "managed block ready in", agents.AgentsFile)
			fprintln(cmd.OutOrStdout(), "\nNext: gh skills add <owner/repo>[@ref]")
			return nil
		},
	}
	rootFlag(cmd, &rootDir)
	return cmd
}
