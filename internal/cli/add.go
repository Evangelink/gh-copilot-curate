package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Evangelink/gh-skill-pack/internal/layout"
	"github.com/Evangelink/gh-skill-pack/internal/manifest"
	"github.com/Evangelink/gh-skill-pack/internal/skills"
	"github.com/Evangelink/gh-skill-pack/internal/source"
)

func newAddCmd() *cobra.Command {
	var (
		rootDir    string
		pluginName string
		includes   []string
		modeStr    string
		layoutStr  string
		dryRun     bool
		yes        bool
		force      bool
	)
	cmd := &cobra.Command{
		Use:   "add <owner/repo[@ref]>",
		Short: "Install or update a plugin from a source repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot(rootDir, false)
			if err != nil {
				return err
			}
			spec, err := source.ParseSpec(args[0])
			if err != nil {
				return err
			}
			mode := manifest.InstallMode(strings.ToLower(strings.TrimSpace(modeStr)))
			if mode != "" && !manifest.IsValidMode(mode) {
				return errf("invalid --mode %q (want summary|inline|link)", modeStr)
			}
			layKind := layout.Kind("")
			if layoutStr != "" {
				switch strings.ToLower(layoutStr) {
				case string(layout.KindDotnetSkills):
					layKind = layout.KindDotnetSkills
				case string(layout.KindHeuristic):
					layKind = layout.KindHeuristic
				default:
					return errf("invalid --layout %q (want dotnet-skills|heuristic)", layoutStr)
				}
			}
			if spec.Ref == "" {
				fprintln(cmd.ErrOrStderr(), "warning: no @ref pinned; will install from the default branch (use a tag or SHA for reproducibility)")
			} else if looksLikeBranch(spec.Ref) {
				fprintln(cmd.ErrOrStderr(), "warning: ref", spec.Ref, "looks like a branch; for reproducibility prefer a tag or SHA")
			}
			if !yes && !dryRun {
				fprintln(cmd.OutOrStdout(), "Will install", spec.String(), "into", root)
				fprintln(cmd.OutOrStdout(), "Re-run with --yes to confirm, or --dry-run to preview.")
				return nil
			}
			ops := &skills.Operations{}
			res, err := ops.Add(context.Background(), skills.AddOptions{
				RepoRoot:   root,
				Spec:       spec,
				PluginName: pluginName,
				Includes:   includes,
				Mode:       mode,
				Layout:     layKind,
				DryRun:     dryRun,
				Force:      force,
			})
			if err != nil {
				return err
			}
			for _, p := range res.Plugins {
				prefix := "installed"
				if dryRun {
					prefix = "[dry-run] would install"
				}
				fprintln(cmd.OutOrStdout(), prefix, p.ID, "@", p.ResolvedSHA, "("+fmt.Sprint(p.FilesWritten), "files,", "layout="+string(p.Layout)+")")
			}
			if !dryRun {
				fprintln(cmd.OutOrStdout(), "wrote", manifest.LockPath)
			}
			return nil
		},
	}
	rootFlag(cmd, &rootDir)
	cmd.Flags().StringVar(&pluginName, "plugin", "", "install only the named plugin from the source")
	cmd.Flags().StringSliceVar(&includes, "include", nil, "limit installed paths to those starting with one of these prefixes (repeatable; comma-separated)")
	cmd.Flags().StringVar(&modeStr, "mode", "", "install mode: summary (default) | inline | link")
	cmd.Flags().StringVar(&layoutStr, "layout", "", "override layout detection: dotnet-skills | heuristic")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview without writing files")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing plugin even if its source differs")
	return cmd
}

// looksLikeBranch is a best-effort check: SHAs and v-prefixed tags don't
// look like branch names. Anything else triggers the reproducibility warning.
func looksLikeBranch(ref string) bool {
	if ref == "" {
		return false
	}
	if strings.HasPrefix(ref, "v") {
		return false
	}
	for _, r := range ref {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return true
		}
	}
	return false
}
