package cli

import (
	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-copilot-curate/internal/skills"
)

func newVerifyCmd() *cobra.Command {
	var rootDir string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check that installed files match the lock and that the managed block is current",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := resolveRoot(rootDir, false)
			if err != nil {
				return err
			}
			ops := &skills.Operations{}
			res, err := ops.Verify(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if res.OK() {
				fprintln(out, "OK — no drift detected.")
				return nil
			}
			for _, f := range res.MissingFiles {
				fprintln(out, "missing:", f)
			}
			for _, f := range res.ModifiedFiles {
				fprintln(out, "modified:", f)
			}
			for _, f := range res.UnknownInLock {
				fprintln(out, "unreadable:", f)
			}
			if res.ManifestStale {
				fprintln(out, "manifest changed since lock; run `gh copilot-curate update`")
			}
			if res.BlockStale {
				fprintln(out, "AGENTS.md managed block is out of date; run `gh copilot-curate update`")
			}
			if res.InstructionsFileStale {
				fprintln(out, ".github/instructions/copilot-curate.instructions.md is out of date; run `gh copilot-curate update`")
			}
			if res.LegacyBlockPresent {
				fprintln(out, ".github/copilot-instructions.md still contains a v0.4 managed block; run any `gh copilot-curate` mutating command (add/update/remove/init) to migrate")
			}
			if res.LegacyLayoutPending {
				fprintln(out, "lock still references the v0.5 layout under .copilot/plugins/; run any `gh copilot-curate` mutating command (add/update/remove) to migrate to .agents/skills/ + .github/agents/")
			}
			return errf("verify failed: %d missing, %d modified, %d unreadable",
				len(res.MissingFiles), len(res.ModifiedFiles), len(res.UnknownInLock))
		},
	}
	rootFlag(cmd, &rootDir)
	return cmd
}
