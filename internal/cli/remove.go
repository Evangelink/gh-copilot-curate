package cli

import (
	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-skill-pack/internal/skills"
)

func newRemoveCmd() *cobra.Command {
	var (
		rootDir string
		force   bool
		dryRun  bool
	)
	cmd := &cobra.Command{
		Use:   "remove <plugin>",
		Short: "Remove an installed plugin (refuses on local drift unless --force)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot(rootDir, false)
			if err != nil {
				return err
			}
			ops := &skills.Operations{}
			res, err := ops.Remove(skills.RemoveOptions{
				RepoRoot: root, PluginName: args[0], Force: force, DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			prefix := "removed"
			if dryRun {
				prefix = "[dry-run] would remove"
			}
			for _, f := range res.Files {
				fprintln(cmd.OutOrStdout(), prefix, f)
			}
			return nil
		},
	}
	rootFlag(cmd, &rootDir)
	cmd.Flags().BoolVar(&force, "force", false, "remove even if files have been locally modified")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview without deleting")
	return cmd
}
