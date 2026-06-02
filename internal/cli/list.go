package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-copilot-curate/internal/manifest"
)

func newListCmd() *cobra.Command {
	var rootDir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := resolveRoot(rootDir, false)
			if err != nil {
				return err
			}
			lock, err := manifest.LoadLock(root)
			if err != nil {
				return err
			}
			if len(lock.Plugins) == 0 {
				fprintln(cmd.OutOrStdout(), "No plugins installed. Run `gh copilot-curate add <owner/repo>` to install one.")
				return nil
			}
			out := cmd.OutOrStdout()
			fprintln(out, fmt.Sprintf("%-30s %-25s %-12s %s", "PLUGIN", "SOURCE", "REF", "MODE"))
			for _, p := range lock.Plugins {
				ref := p.RequestedRef
				if ref == "" {
					ref = shortSHA(p.ResolvedRef)
				}
				fprintln(out, fmt.Sprintf("%-30s %-25s %-12s %s",
					p.ID,
					p.Source.Owner+"/"+p.Source.Repo,
					ref,
					string(p.Install.Mode),
				))
			}
			return nil
		},
	}
	rootFlag(cmd, &rootDir)
	return cmd
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
