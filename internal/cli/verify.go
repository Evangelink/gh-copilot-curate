package cli

import (
	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-agent-pack/internal/skills"
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
				fprintln(out, "manifest changed since lock; run `gh agent-pack update`")
			}
			if res.BlockStale {
				fprintln(out, "AGENTS.md managed block is out of date; run `gh agent-pack update`")
			}
			if res.CopilotBlockStale {
				fprintln(out, ".github/copilot-instructions.md managed block is out of date; run `gh agent-pack update`")
			}
			return errf("verify failed: %d missing, %d modified, %d unreadable",
				len(res.MissingFiles), len(res.ModifiedFiles), len(res.UnknownInLock))
		},
	}
	rootFlag(cmd, &rootDir)
	return cmd
}
