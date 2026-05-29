package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-skill-pack/internal/skills"
)

func newUpdateCmd() *cobra.Command {
	var (
		rootDir string
		force   bool
		dryRun  bool
	)
	cmd := &cobra.Command{
		Use:   "update [plugin...]",
		Short: "Re-fetch and re-install plugins at their manifest ref (refuses on drift unless --force)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot(rootDir, false)
			if err != nil {
				return err
			}
			ops := &skills.Operations{}
			res, err := ops.Update(context.Background(), skills.UpdateOptions{
				RepoRoot:    root,
				PluginNames: args,
				Force:       force,
				DryRun:      dryRun,
			})
			if err != nil {
				return err
			}
			for _, p := range res.Plugins {
				prefix := "updated"
				if dryRun {
					prefix = "[dry-run] would update"
				}
				fprintln(cmd.OutOrStdout(), prefix, p.ID, "@", shortSHA(p.ResolvedSHA))
			}
			return nil
		},
	}
	rootFlag(cmd, &rootDir)
	cmd.Flags().BoolVar(&force, "force", false, "overwrite locally-modified files")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview without writing files")
	return cmd
}
