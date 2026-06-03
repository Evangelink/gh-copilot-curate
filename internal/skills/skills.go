// Package skills orchestrates plugin install / update / verify / remove
// using the manifest, source, layout, and agents subpackages.
//
// All operations are repo-scoped in v1 (--scope=user is deferred). The
// orchestrator is IO-heavy but kept side-effect-isolated behind the
// Operations type so commands can be unit-tested.
//
// TODO(v1.1): Add/Update perform per-file writes interleaved with manifest
// and lock updates. A failure partway through can leave orphaned files
// under .copilot/plugins/<id>/ without a corresponding lock entry. We
// considered staging into a temp tree and committing atomically; for v1 the
// recovery path is "re-run `gh copilot-curate add` / `gh copilot-curate update` to reach a
// consistent state, or delete the orphaned directory by hand". Track in
// roadmap when we hit a real-world incident.
package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Evangelink/gh-copilot-curate/internal/agents"
	"github.com/Evangelink/gh-copilot-curate/internal/layout"
	"github.com/Evangelink/gh-copilot-curate/internal/manifest"
	"github.com/Evangelink/gh-copilot-curate/internal/repo"
	"github.com/Evangelink/gh-copilot-curate/internal/source"
)

// ToolVersion is overridden at link time by GoReleaser.
var ToolVersion = "dev"

// Operations bundles the IO dependencies. Tests can replace Fetcher with a
// stub Fetcher implementation.
type Operations struct {
	Fetcher source.Fetcher
}

// AddOptions controls the `add` command.
type AddOptions struct {
	RepoRoot   string
	Spec       source.Spec
	PluginName string         // optional --plugin filter
	Includes   []string       // optional --include
	Mode       manifest.InstallMode
	Layout     layout.Kind    // optional --layout override
	DryRun     bool
	Force      bool           // overwrite existing plugin without confirmation
}

// AddResult summarises what `add` did (or would do, in dry-run mode).
type AddResult struct {
	Plugins             []PluginChange
	Manifest            *manifest.Manifest
	Lock                *manifest.Lock
	AgentsChanged       bool
	InstructionsChanged bool
	// LegacyBlockCleaned is true when this command removed the v0.4-era
	// managed block from .github/copilot-instructions.md as part of the
	// v0.5 migration. LegacyFileDeleted is true when that file was also
	// removed because it had no other content. Used by the CLI to print a
	// one-shot migration notice.
	LegacyBlockCleaned bool
	LegacyFileDeleted  bool
	// LegacyLayoutMigrated is true when this command moved files from the
	// v0.4-v0.5 .copilot/plugins/<plugin>/... tree into the canonical
	// .agents/skills/ and .github/agents/ locations. The CLI prints a
	// one-shot notice when this fires.
	LegacyLayoutMigrated bool
}

// PluginChange describes the change for a single installed plugin.
type PluginChange struct {
	ID           string
	ResolvedSHA  string
	Layout       layout.Kind
	FilesWritten int
	Skipped      bool // already at desired state
}

// Add fetches the spec, detects layout, copies files, updates manifest +
// lock, and rewrites the AGENTS.md managed block. Honors opts.DryRun.
func (ops *Operations) Add(ctx context.Context, opts AddOptions) (*AddResult, error) {
	if ops.Fetcher == nil {
		ops.Fetcher = source.NewHTTPFetcher()
	}
	if opts.RepoRoot == "" {
		return nil, errors.New("repo root not set")
	}
	mode := opts.Mode
	if mode == "" {
		mode = manifest.ModeSummary
	}
	if !manifest.IsValidMode(mode) {
		return nil, fmt.Errorf("invalid install mode %q", mode)
	}

	sha, err := ops.Fetcher.ResolveRef(ctx, opts.Spec)
	if err != nil {
		return nil, fmt.Errorf("resolve ref: %w", err)
	}

	tmp, err := os.MkdirTemp("", "gh-copilot-curate-fetch-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := ops.Fetcher.DownloadTree(ctx, opts.Spec, sha, tmp); err != nil {
		return nil, fmt.Errorf("download tarball: %w", err)
	}
	upstreamRoot, err := source.FindExtractedRoot(tmp)
	if err != nil {
		return nil, err
	}

	kind := opts.Layout
	if kind == "" {
		k, err := layout.Detect(upstreamRoot)
		if err != nil {
			return nil, fmt.Errorf("layout detect: %w", err)
		}
		kind = k
	}

	plugins, err := layout.Translate(upstreamRoot, kind, opts.PluginName, opts.Includes)
	if err != nil {
		return nil, fmt.Errorf("layout translate: %w", err)
	}
	if len(plugins) == 0 {
		return nil, errors.New("no plugins matched in upstream tree")
	}

	mf, err := manifest.LoadManifest(opts.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	lock, err := manifest.LoadLock(opts.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("load lock: %w", err)
	}

	// v0.5 → v0.6 auto-migration. Runs before any new writes so the lock
	// matches on-disk reality when we compute collisions.
	migrated, err := migrateLegacyPluginsLayout(opts.RepoRoot, lock, opts.Force, opts.DryRun)
	if err != nil {
		return nil, err
	}

	// Preflight collision check: every incoming destination must be
	// (a) writable (no untracked file in the way unless --force) and
	// (b) unique across incoming plugins + other locked plugins.
	if err := checkInstallCollisions(opts.RepoRoot, plugins, lock, opts.Force); err != nil {
		return nil, err
	}

	res := &AddResult{Manifest: mf, Lock: lock, LegacyLayoutMigrated: migrated}

	for _, p := range plugins {
		change, err := installPlugin(ctx, opts, upstreamRoot, p, kind, sha, mf, lock)
		if err != nil {
			return res, err
		}
		res.Plugins = append(res.Plugins, change)
	}

	if opts.DryRun {
		return res, nil
	}

	if err := EnsurePackGitAttributes(opts.RepoRoot); err != nil {
		return res, fmt.Errorf("write .copilot/.gitattributes: %w", err)
	}
	if err := manifest.SaveManifest(opts.RepoRoot, mf); err != nil {
		return res, fmt.Errorf("save manifest: %w", err)
	}
	if err := writeLock(opts.RepoRoot, mf, lock); err != nil {
		return res, err
	}

	if err := rewriteAgentsBlocks(opts.RepoRoot, lock, res); err != nil {
		return res, err
	}
	return res, nil
}

// installPlugin copies one plugin's files into the repo and upserts the
// manifest + lock entries. Mutates mf and lock in place.
func installPlugin(
	_ context.Context,
	opts AddOptions,
	upstreamRoot string,
	p layout.Plugin,
	kind layout.Kind,
	sha string,
	mf *manifest.Manifest,
	lock *manifest.Lock,
) (PluginChange, error) {
	id := p.Name

	existing := mf.FindPlugin(id)
	if existing != nil && !opts.Force {
		// Allow re-install if the spec matches (idempotent). Reject if a
		// different source is requested for the same id.
		if existing.Source != opts.Spec.Owner+"/"+opts.Spec.Repo {
			return PluginChange{ID: id}, fmt.Errorf(
				"plugin %q already exists from source %q; pass --force to overwrite",
				id, existing.Source)
		}
	}

	mf.Upsert(manifest.Plugin{
		ID:      id,
		Source:  opts.Spec.Owner + "/" + opts.Spec.Repo,
		Ref:     opts.Spec.Ref,
		Include: opts.Includes,
		Install: &manifest.InstallConfig{Mode: chooseMode(opts.Mode, existing)},
	})

	var lockFiles []manifest.LockFile
	for _, f := range p.Files {
		srcPath := filepath.Join(upstreamRoot, filepath.FromSlash(f.UpstreamPath))
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return PluginChange{ID: id}, fmt.Errorf("read upstream file %s: %w", f.UpstreamPath, err)
		}
		upHash := hashBytes(data)
		canonical := f.CanonicalPath
		if !opts.DryRun {
			if err := writeFileAtomic(filepath.Join(opts.RepoRoot, filepath.FromSlash(canonical)), data, f.Mode); err != nil {
				return PluginChange{ID: id}, err
			}
		}
		lockFiles = append(lockFiles, manifest.LockFile{
			Path:         canonical,
			UpstreamPath: f.UpstreamPath,
			UpstreamHash: upHash,
			LocalHash:    upHash, // fresh write — local == upstream
			Mode:         fmt.Sprintf("%04o", f.Mode.Perm()),
		})
	}

	lock.Upsert(manifest.LockPlugin{
		ID: id,
		Source: manifest.LockSource{
			Type: "github", Host: opts.Spec.Host, Owner: opts.Spec.Owner, Repo: opts.Spec.Repo,
		},
		RequestedRef: opts.Spec.Ref,
		ResolvedRef:  sha,
		Layout:       string(kind),
		Install: manifest.InstallConfig{
			Mode: chooseMode(opts.Mode, existing),
		},
		Files: lockFiles,
	})
	return PluginChange{ID: id, ResolvedSHA: sha, Layout: kind, FilesWritten: len(p.Files)}, nil
}

func chooseMode(requested manifest.InstallMode, existing *manifest.Plugin) manifest.InstallMode {
	if requested != "" {
		return requested
	}
	if existing != nil && existing.Install != nil && existing.Install.Mode != "" {
		return existing.Install.Mode
	}
	return manifest.ModeSummary
}

// RemoveOptions controls the `remove` command.
type RemoveOptions struct {
	RepoRoot   string
	PluginName string
	Force      bool
	DryRun     bool
}

// RemoveResult reports what was removed.
type RemoveResult struct {
	Files                []string
	DriftDetected        bool
	Manifest             *manifest.Manifest
	Lock                 *manifest.Lock
	LegacyBlockCleaned   bool
	LegacyFileDeleted    bool
	LegacyLayoutMigrated bool
}

// Remove deletes a plugin's files. If any file is locally modified, refuses
// unless opts.Force is set.
func (ops *Operations) Remove(opts RemoveOptions) (*RemoveResult, error) {
	mf, err := manifest.LoadManifest(opts.RepoRoot)
	if err != nil {
		return nil, err
	}
	lock, err := manifest.LoadLock(opts.RepoRoot)
	if err != nil {
		return nil, err
	}
	// v0.5 → v0.6 auto-migration so subsequent path lookups hit the
	// canonical locations. Honors opts.Force for drifted legacy files.
	migrated, err := migrateLegacyPluginsLayout(opts.RepoRoot, lock, opts.Force, opts.DryRun)
	if err != nil {
		return nil, err
	}
	lp := lock.FindPlugin(opts.PluginName)
	if lp == nil {
		return nil, fmt.Errorf("plugin %q not installed", opts.PluginName)
	}

	// Reject any lock entry whose path escapes the repo root *before* doing
	// any reads/writes/deletes. A malicious checked-in lock file could
	// otherwise trick us into touching files outside the repo.
	for _, f := range lp.Files {
		if _, err := repo.MustCleanRel(opts.RepoRoot, f.Path); err != nil {
			return nil, fmt.Errorf("lock contains unsafe path %q: %w", f.Path, err)
		}
	}

	res := &RemoveResult{Manifest: mf, Lock: lock, LegacyLayoutMigrated: migrated}
	var driftFiles []string
	for _, f := range lp.Files {
		full := safeLockPath(opts.RepoRoot, f.Path)
		data, err := os.ReadFile(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return res, err
		}
		if hashBytes(data) != f.LocalHash {
			driftFiles = append(driftFiles, f.Path)
		}
		res.Files = append(res.Files, f.Path)
	}
	if len(driftFiles) > 0 && !opts.Force {
		res.DriftDetected = true
		return res, fmt.Errorf("plugin %q has %d locally-modified file(s); pass --force to remove anyway:\n  - %s",
			opts.PluginName, len(driftFiles), strings.Join(driftFiles, "\n  - "))
	}

	if opts.DryRun {
		return res, nil
	}
	for _, f := range lp.Files {
		full := safeLockPath(opts.RepoRoot, f.Path)
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return res, err
		}
	}
	// Prune empty directories left behind, but never delete the canonical
	// install roots themselves — they're shared with other plugins and
	// with user-installed `gh skill install --scope=project`.
	skillsRootAbs := filepath.Join(opts.RepoRoot, filepath.FromSlash(manifest.SkillsRoot))
	agentsRootAbs := filepath.Join(opts.RepoRoot, filepath.FromSlash(manifest.AgentsRoot))
	for _, f := range lp.Files {
		parent := filepath.Dir(safeLockPath(opts.RepoRoot, f.Path))
		// Pick the appropriate boundary based on which root the file lives
		// under; fall back to repo root for paths outside both (defensive).
		switch {
		case strings.HasPrefix(filepath.ToSlash(f.Path), manifest.SkillsRoot+"/"):
			pruneEmptyDirs(parent, skillsRootAbs)
		case strings.HasPrefix(filepath.ToSlash(f.Path), manifest.AgentsRoot+"/"):
			pruneEmptyDirs(parent, agentsRootAbs)
		default:
			pruneEmptyDirs(parent, opts.RepoRoot)
		}
	}

	mf.Remove(opts.PluginName)
	lock.Remove(opts.PluginName)
	if err := manifest.SaveManifest(opts.RepoRoot, mf); err != nil {
		return res, err
	}
	if err := writeLock(opts.RepoRoot, mf, lock); err != nil {
		return res, err
	}
	rw, err := rewriteAgentsBlocksAfterChange(opts.RepoRoot, lock)
	if err != nil {
		return res, err
	}
	res.LegacyBlockCleaned = rw.LegacyBlockCleaned
	res.LegacyFileDeleted = rw.LegacyFileDeleted
	return res, nil
}

// VerifyResult reports drift between the lock and the on-disk state.
type VerifyResult struct {
	MissingFiles         []string
	ModifiedFiles        []string
	UnknownInLock        []string // files in lock with bad path / unreadable
	ManifestStale        bool
	BlockStale           bool
	InstructionsFileStale bool
	// LegacyBlockPresent flags that .github/copilot-instructions.md still
	// carries a v0.4-era managed block. Verify only reports this; the
	// migration runs on the next mutating command (add/update/remove/init).
	LegacyBlockPresent bool
	// LegacyLayoutPending flags that the lock still references files under
	// .copilot/plugins/<plugin>/... (v0.4-v0.5 layout). Verify only
	// reports; the v0.5→v0.6 migration runs on the next mutating command.
	LegacyLayoutPending bool
}

// OK reports whether the verify result is clean.
func (v VerifyResult) OK() bool {
	return len(v.MissingFiles) == 0 && len(v.ModifiedFiles) == 0 && len(v.UnknownInLock) == 0 &&
		!v.ManifestStale && !v.BlockStale && !v.InstructionsFileStale &&
		!v.LegacyBlockPresent && !v.LegacyLayoutPending
}

// Verify checks that every locked file exists with matching hash, that the
// manifest hash matches the lock's, and that the AGENTS.md managed block
// and the path-specific instructions file are current. It also reports
// when the legacy v0.4 managed block in .github/copilot-instructions.md
// is still present (a migration is pending).
func (ops *Operations) Verify(repoRoot string) (*VerifyResult, error) {
	mf, err := manifest.LoadManifest(repoRoot)
	if err != nil {
		return nil, err
	}
	lock, err := manifest.LoadLock(repoRoot)
	if err != nil {
		return nil, err
	}
	res := &VerifyResult{}
	mfHash, err := manifest.HashManifest(mf)
	if err != nil {
		return nil, err
	}
	if lock.ManifestHash != "" && lock.ManifestHash != mfHash {
		res.ManifestStale = true
	}
	for _, p := range lock.Plugins {
		for _, f := range p.Files {
			if _, perr := repo.MustCleanRel(repoRoot, f.Path); perr != nil {
				res.UnknownInLock = append(res.UnknownInLock, f.Path)
				continue
			}
			full := safeLockPath(repoRoot, f.Path)
			data, err := os.ReadFile(full)
			if err != nil {
				if os.IsNotExist(err) {
					res.MissingFiles = append(res.MissingFiles, f.Path)
					continue
				}
				res.UnknownInLock = append(res.UnknownInLock, f.Path)
				continue
			}
			if hashBytes(data) != f.LocalHash {
				res.ModifiedFiles = append(res.ModifiedFiles, f.Path)
			}
		}
	}
	entries := BuildEntries(repoRoot, lock)
	ok, err := agents.ManagedBlockMatches(repoRoot, agents.AgentsFile, entries)
	if err != nil {
		return res, err
	}
	if !ok {
		res.BlockStale = true
	}
	// Path-specific instructions file: drift is reported when the file
	// contents don't match what WriteInstructionsFile would produce. The
	// helper handles the "missing file with no entries → match" semantics.
	instOK, err := agents.InstructionsFileMatches(repoRoot, entries)
	if err != nil {
		return res, err
	}
	if !instOK {
		res.InstructionsFileStale = true
	}
	// Legacy v0.4 managed block: report when still present so users see
	// they need to run a mutating command (or `init`) to migrate. We don't
	// mutate from verify itself.
	legacyFull := filepath.Join(repoRoot, filepath.FromSlash(agents.LegacyCopilotInstFile))
	if data, statErr := os.ReadFile(legacyFull); statErr == nil {
		if bytes.Contains(data, []byte(agents.BeginMarker)) && bytes.Contains(data, []byte(agents.EndMarker)) {
			res.LegacyBlockPresent = true
		}
	}
	// Legacy v0.5 layout: any lock entry under .copilot/plugins/ means the
	// v0.5→v0.6 migration hasn't run yet on this repo.
	for _, p := range lock.Plugins {
		for _, f := range p.Files {
			if strings.HasPrefix(filepath.ToSlash(f.Path), manifest.LegacyPluginsDir+"/") {
				res.LegacyLayoutPending = true
				break
			}
		}
		if res.LegacyLayoutPending {
			break
		}
	}
	sort.Strings(res.MissingFiles)
	sort.Strings(res.ModifiedFiles)
	sort.Strings(res.UnknownInLock)
	return res, nil
}

// UpdateOptions controls the `update` command. v1 supports `--no-merge`
// semantics only: if any installed file has been locally modified, the
// command refuses unless --force.
type UpdateOptions struct {
	RepoRoot    string
	PluginNames []string // empty = all
	Force       bool
	DryRun      bool
}

// UpdateResult summarises an update run.
type UpdateResult struct {
	Plugins              []PluginChange
	LegacyBlockCleaned   bool
	LegacyFileDeleted    bool
	LegacyLayoutMigrated bool
}

// Update re-fetches each plugin at its manifest ref and rewrites files.
func (ops *Operations) Update(ctx context.Context, opts UpdateOptions) (*UpdateResult, error) {
	if ops.Fetcher == nil {
		ops.Fetcher = source.NewHTTPFetcher()
	}
	mf, err := manifest.LoadManifest(opts.RepoRoot)
	if err != nil {
		return nil, err
	}
	lock, err := manifest.LoadLock(opts.RepoRoot)
	if err != nil {
		return nil, err
	}

	wanted := opts.PluginNames
	if len(wanted) == 0 {
		for _, p := range mf.Plugins {
			wanted = append(wanted, p.ID)
		}
	}

	res := &UpdateResult{}
	for _, id := range wanted {
		mp := mf.FindPlugin(id)
		if mp == nil {
			return res, fmt.Errorf("plugin %q not in manifest", id)
		}
		lp := lock.FindPlugin(id)
		if lp != nil && !opts.Force {
			// Check lock paths before reading anything, so a bad lock entry
			// is rejected up-front rather than silently skipped.
			for _, f := range lp.Files {
				if _, err := repo.MustCleanRel(opts.RepoRoot, f.Path); err != nil {
					return res, fmt.Errorf("plugin %q lock contains unsafe path %q: %w", id, f.Path, err)
				}
			}
			var drifted []string
			for _, f := range lp.Files {
				full := safeLockPath(opts.RepoRoot, f.Path)
				data, err := os.ReadFile(full)
				if err == nil && hashBytes(data) != f.LocalHash {
					drifted = append(drifted, f.Path)
				}
			}
			if len(drifted) > 0 {
				return res, fmt.Errorf("plugin %q has %d locally-modified file(s); pass --force to overwrite:\n  - %s",
					id, len(drifted), strings.Join(drifted, "\n  - "))
			}
		}

		spec, err := source.ParseSpec(mp.Source + atRef(mp.Ref))
		if err != nil {
			return res, err
		}
		var mode manifest.InstallMode
		if mp.Install != nil {
			mode = mp.Install.Mode
		}
		addRes, err := ops.Add(ctx, AddOptions{
			RepoRoot:   opts.RepoRoot,
			Spec:       spec,
			PluginName: mp.ID,
			Includes:   mp.Include,
			Mode:       mode,
			DryRun:     opts.DryRun,
			Force:      true,
		})
		if err != nil {
			return res, err
		}
		res.Plugins = append(res.Plugins, addRes.Plugins...)
		// Surface the v0.4 → v0.5 migration on whichever Add actually
		// performed the cleanup (only the first call will, by definition).
		if addRes.LegacyBlockCleaned {
			res.LegacyBlockCleaned = true
		}
		if addRes.LegacyFileDeleted {
			res.LegacyFileDeleted = true
		}
		if addRes.LegacyLayoutMigrated {
			res.LegacyLayoutMigrated = true
		}
	}
	return res, nil
}

func atRef(ref string) string {
	if ref == "" {
		return ""
	}
	return "@" + ref
}

// BuildEntries flattens lock plugins into a slice of agents.Entry for the
// managed block. Each SKILL.md and *.agent.md becomes one row, populated
// with title+summary extracted from the on-disk file. Files that fail to
// read are skipped (verify catches them separately). Lock entries with
// unsafe paths are also skipped silently — verify will surface them.
func BuildEntries(repoRoot string, lock *manifest.Lock) []agents.Entry {
	var entries []agents.Entry
	for _, p := range lock.Plugins {
		mode := string(p.Install.Mode)
		if mode == "" {
			mode = "summary"
		}
		for _, f := range p.Files {
			base := path.Base(f.Path)
			isAgent := strings.HasSuffix(base, ".agent.md")
			isSkill := base == "SKILL.md"
			if !isAgent && !isSkill {
				continue
			}
			if _, err := repo.MustCleanRel(repoRoot, f.Path); err != nil {
				continue
			}
			full := safeLockPath(repoRoot, f.Path)
			title, summary, err := agents.SummarizeFile(full)
			if err != nil {
				continue
			}
			entry := agents.Entry{
				Plugin:  p.ID,
				Title:   title,
				Summary: summary,
				Link:    f.Path,
				IsAgent: isAgent,
				Mode:    mode,
			}
			if mode == "inline" {
				entry.FullPath = full
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

// rewriteResult bundles outputs from one full curate-managed write pass:
// AGENTS.md block, the path-specific instructions file, and any v0.4
// legacy migration that happened along the way. The CLI surfaces the
// LegacyBlockCleaned / LegacyFileDeleted flags as a one-shot notice.
type rewriteResult struct {
	AgentsChanged       bool
	InstructionsChanged bool
	LegacyBlockCleaned  bool
	LegacyFileDeleted   bool
}

// rewriteCurateManagedFiles updates every file gh-copilot-curate owns to
// reflect the current lock contents:
//
//  1. Rewrites the AGENTS.md managed block (preserving user content
//     outside the markers).
//  2. Rewrites .github/instructions/copilot-curate.instructions.md
//     wholesale, regenerating the YAML front-matter and inventory body.
//  3. Removes any v0.4-era managed block from
//     .github/copilot-instructions.md, deleting the file if it would
//     otherwise be empty.
//
// All three steps run for every mutating command (add / update / remove /
// init bootstrap) so a single user-facing operation always leaves the
// repo in a consistent v0.5 state.
func rewriteCurateManagedFiles(repoRoot string, lock *manifest.Lock) (rewriteResult, error) {
	entries := BuildEntries(repoRoot, lock)
	var out rewriteResult
	a, err := agents.WriteManagedBlock(repoRoot, agents.AgentsFile, entries)
	if err != nil {
		return out, err
	}
	out.AgentsChanged = a
	i, err := agents.WriteInstructionsFile(repoRoot, entries)
	if err != nil {
		return out, err
	}
	out.InstructionsChanged = i
	cleaned, deleted, err := agents.CleanLegacyCopilotInstructionsBlock(repoRoot)
	if err != nil {
		return out, err
	}
	out.LegacyBlockCleaned = cleaned
	out.LegacyFileDeleted = deleted
	return out, nil
}

func rewriteAgentsBlocks(repoRoot string, lock *manifest.Lock, res *AddResult) error {
	r, err := rewriteCurateManagedFiles(repoRoot, lock)
	if err != nil {
		return err
	}
	res.AgentsChanged = r.AgentsChanged
	res.InstructionsChanged = r.InstructionsChanged
	res.LegacyBlockCleaned = r.LegacyBlockCleaned
	res.LegacyFileDeleted = r.LegacyFileDeleted
	return nil
}

// rewriteAgentsBlocksAfterChange is used by update/remove paths that don't
// surface an AddResult to the caller. Returns the rewrite metadata so the
// CLI can still print the legacy-migration notice when applicable.
func rewriteAgentsBlocksAfterChange(repoRoot string, lock *manifest.Lock) (rewriteResult, error) {
	return rewriteCurateManagedFiles(repoRoot, lock)
}

func writeLock(repoRoot string, mf *manifest.Manifest, lock *manifest.Lock) error {
	lock.Version = manifest.SchemaVersion
	lock.ManagedBy = manifest.ManagedBy
	lock.ToolVersion = ToolVersion
	lock.GeneratedAt = time.Now().UTC()
	h, err := manifest.HashManifest(mf)
	if err != nil {
		return err
	}
	lock.ManifestHash = h
	return manifest.SaveLock(repoRoot, lock)
}

func writeFileAtomic(full string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	// Randomized temp name in the destination dir, so a pre-existing
	// predictable temp path can't be used to redirect the write via a
	// symlink before we rename.
	tmp, err := os.CreateTemp(filepath.Dir(full), filepath.Base(full)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, full)
}

// safeLockPath joins a validated repo-relative lock path to the repo root.
// Callers MUST have already validated the path via repo.MustCleanRel.
func safeLockPath(repoRoot, lockRel string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(lockRel))
}

// EnsurePackGitAttributes ensures that gh-copilot-curate's managed files
// have stable byte content across CRLF/LF checkouts, so LocalHash
// comparison in verify doesn't produce spurious drift.
//
// It writes two files (both idempotent):
//
//  1. .copilot/.gitattributes with `* text eol=lf` so the manifest, lock,
//     and any future tool-state files keep stable bytes.
//  2. The repo-root .gitattributes file, in a fenced gh-copilot-curate
//     managed block, covering the canonical project-scope install roots
//     (.agents/skills/** and .github/agents/**). User-authored entries
//     outside the fence are preserved.
func EnsurePackGitAttributes(repoRoot string) error {
	// 1. .copilot/.gitattributes
	rel := filepath.Join(manifest.PackRoot, ".gitattributes")
	full := filepath.Join(repoRoot, rel)
	want := []byte("# managed by gh-copilot-curate: keep stable byte content across CRLF/LF checkouts\n* text eol=lf\n")
	existing, err := os.ReadFile(full)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !bytes.Equal(existing, want) {
		if err := writeFileAtomic(full, want, 0o644); err != nil {
			return err
		}
	}
	// 2. repo-root .gitattributes — managed block.
	return ensureRootGitAttributesBlock(repoRoot)
}

const (
	rootGitAttrBegin = "# BEGIN gh-copilot-curate managed"
	rootGitAttrEnd   = "# END gh-copilot-curate managed"
)

// ensureRootGitAttributesBlock writes (or refreshes) a fenced managed block
// in the repo-root .gitattributes file covering the canonical install
// roots. The block is inserted at the end of the file when absent and is
// surgically replaced in place when present.
func ensureRootGitAttributesBlock(repoRoot string) error {
	full := filepath.Join(repoRoot, ".gitattributes")
	wantBlock := rootGitAttrBegin + "\n" +
		"# Keep installed skills and agents at stable bytes across CRLF/LF checkouts\n" +
		"# so gh copilot-curate verify does not report spurious drift.\n" +
		".agents/skills/** text eol=lf\n" +
		".github/agents/** text eol=lf\n" +
		rootGitAttrEnd + "\n"

	existing, err := os.ReadFile(full)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// File missing — write just the block.
	if err != nil {
		return writeFileAtomic(full, []byte(wantBlock), 0o644)
	}
	// Find and replace existing block, or append.
	beginIdx := bytes.Index(existing, []byte(rootGitAttrBegin))
	endIdx := bytes.Index(existing, []byte(rootGitAttrEnd))
	if beginIdx >= 0 && endIdx > beginIdx {
		// Replace from beginIdx through end-of-line of the END marker.
		tailStart := endIdx + len(rootGitAttrEnd)
		if tailStart < len(existing) && existing[tailStart] == '\n' {
			tailStart++
		}
		next := make([]byte, 0, len(existing)+len(wantBlock))
		next = append(next, existing[:beginIdx]...)
		next = append(next, wantBlock...)
		next = append(next, existing[tailStart:]...)
		if bytes.Equal(next, existing) {
			return nil
		}
		return writeFileAtomic(full, next, 0o644)
	}
	// Append, ensuring a separating newline.
	var next bytes.Buffer
	next.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		next.WriteByte('\n')
	}
	if len(existing) > 0 {
		next.WriteByte('\n')
	}
	next.WriteString(wantBlock)
	return writeFileAtomic(full, next.Bytes(), 0o644)
}

// pruneEmptyDirs walks up from `dir` and removes empty directories. It
// stops at `stopAt` (exclusive) — that directory is never removed even if
// empty. Both `dir` and `stopAt` must be absolute paths in normalized form.
// Passing an empty `stopAt` disables the boundary check.
func pruneEmptyDirs(dir, stopAt string) {
	for d := dir; d != "" && d != filepath.Dir(d); d = filepath.Dir(d) {
		if stopAt != "" && d == stopAt {
			return
		}
		entries, err := os.ReadDir(d)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(d); err != nil {
			return
		}
	}
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// migrateLegacyPluginsLayout migrates a v0.4–v0.5 install (where files
// lived under .copilot/plugins/<plugin>/{skills,agents,...}) to the v0.6
// canonical layout (.agents/skills/<skill>/... and .github/agents/...).
//
// For each lock entry whose Path is under .copilot/plugins/:
//
//  1. Compute the new canonical path. Lock entries whose path can't be
//     mapped (orphaned, plugin metadata) are dropped from the lock.
//  2. Compare the on-disk bytes against LockFile.LocalHash. If they
//     differ, the file has been hand-edited — refuse unless force=true so
//     we don't bless local edits as the new managed state.
//  3. Move the file (rename) to the new path. LocalHash is preserved
//     unchanged; only the Path field is rewritten.
//  4. After all moves succeed, save the lock and prune .copilot/plugins/.
//
// In dry-run mode no changes are made; the function reports whether a
// migration WOULD occur via the return value but skips IO. Returns
// (true, nil) when at least one file was migrated (or would be in
// dry-run), (false, nil) when no migration was needed.
//
// IMPORTANT: this mutates `lock` in place when migration runs, so the
// caller's subsequent reads (e.g. plugin file iteration) see the new
// paths. The lock is also written to disk on success.
func migrateLegacyPluginsLayout(repoRoot string, lock *manifest.Lock, force, dryRun bool) (bool, error) {
	// Quick scan: any lock paths under .copilot/plugins/?
	type pendingMove struct {
		pluginIdx int
		fileIdx   int
		oldPath   string
		newPath   string
	}
	var moves []pendingMove
	var orphans []struct{ pluginIdx, fileIdx int }
	for pi := range lock.Plugins {
		for fi := range lock.Plugins[pi].Files {
			f := lock.Plugins[pi].Files[fi]
			ps := filepath.ToSlash(f.Path)
			if !strings.HasPrefix(ps, manifest.LegacyPluginsDir+"/") {
				continue
			}
			// Strip ".copilot/plugins/<plugin>/" prefix; the remainder is
			// the plugin-relative path that canonicalForDotnetSkills would
			// produce at install time.
			rest := ps[len(manifest.LegacyPluginsDir)+1:]
			// rest = "<plugin>/<...>"; drop leading "<plugin>/".
			slash := strings.IndexByte(rest, '/')
			if slash < 0 {
				orphans = append(orphans, struct{ pluginIdx, fileIdx int }{pi, fi})
				continue
			}
			relInPlugin := rest[slash+1:]
			newCanonical, ok := mapLegacyPluginRelToCanonical(relInPlugin)
			if !ok {
				// Plugin-level metadata or anything outside skills//agents/.
				// Drop from the lock — v0.6 doesn't track it.
				orphans = append(orphans, struct{ pluginIdx, fileIdx int }{pi, fi})
				continue
			}
			moves = append(moves, pendingMove{pi, fi, f.Path, newCanonical})
		}
	}
	if len(moves) == 0 && len(orphans) == 0 {
		return false, nil
	}
	if dryRun {
		return true, nil
	}

	// Drift check first — never overwrite (or move) a hand-edited file
	// without explicit consent.
	var drifted []string
	for _, m := range moves {
		oldFull := filepath.Join(repoRoot, filepath.FromSlash(m.oldPath))
		data, err := os.ReadFile(oldFull)
		if err != nil {
			if os.IsNotExist(err) {
				// File missing — nothing to move; the new install will
				// recreate it. Skip silently.
				continue
			}
			return false, fmt.Errorf("read %s: %w", m.oldPath, err)
		}
		want := lock.Plugins[m.pluginIdx].Files[m.fileIdx].LocalHash
		if want != "" && hashBytes(data) != want {
			drifted = append(drifted, m.oldPath)
		}
	}
	if len(drifted) > 0 && !force {
		return false, fmt.Errorf("legacy v0.5 layout has %d locally-modified file(s); pass --force to migrate anyway:\n  - %s",
			len(drifted), strings.Join(drifted, "\n  - "))
	}

	// Perform moves. We rename instead of copy+delete so file modes and
	// inodes are preserved.
	for _, m := range moves {
		oldFull := filepath.Join(repoRoot, filepath.FromSlash(m.oldPath))
		newFull := filepath.Join(repoRoot, filepath.FromSlash(m.newPath))
		if err := os.MkdirAll(filepath.Dir(newFull), 0o755); err != nil {
			return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(m.newPath), err)
		}
		if _, err := os.Stat(oldFull); os.IsNotExist(err) {
			// Source missing; just update the lock entry so the install
			// step recreates it at the new path.
			lock.Plugins[m.pluginIdx].Files[m.fileIdx].Path = m.newPath
			continue
		}
		if err := os.Rename(oldFull, newFull); err != nil {
			return false, fmt.Errorf("move %s → %s: %w", m.oldPath, m.newPath, err)
		}
		lock.Plugins[m.pluginIdx].Files[m.fileIdx].Path = m.newPath
	}

	// Drop orphaned entries (plugin metadata, malformed paths). Process in
	// reverse index order per plugin so the indices stay stable.
	if len(orphans) > 0 {
		// Group by plugin and sort descending fileIdx.
		byPlugin := map[int][]int{}
		for _, o := range orphans {
			byPlugin[o.pluginIdx] = append(byPlugin[o.pluginIdx], o.fileIdx)
		}
		for pi, fis := range byPlugin {
			sort.Sort(sort.Reverse(sort.IntSlice(fis)))
			files := lock.Plugins[pi].Files
			for _, fi := range fis {
				// Delete the orphaned file on disk as well so the user
				// isn't left with a stray .copilot/plugins/<x>/plugin.json.
				oldFull := filepath.Join(repoRoot, filepath.FromSlash(files[fi].Path))
				_ = os.Remove(oldFull)
				files = append(files[:fi], files[fi+1:]...)
			}
			lock.Plugins[pi].Files = files
		}
	}

	// Prune the entire .copilot/plugins/ tree. After the moves, only
	// metadata files we don't track would remain — but those are user
	// curation artifacts and should have already been removed in the
	// orphan-cleanup loop above. Walk top-down and remove empty dirs.
	pluginsAbs := filepath.Join(repoRoot, filepath.FromSlash(manifest.LegacyPluginsDir))
	removeEmptyTree(pluginsAbs)

	return true, nil
}

// mapLegacyPluginRelToCanonical mirrors canonicalForDotnetSkills (in the
// layout package) but is duplicated here so the migration path doesn't
// take an import cycle on layout from skills.
func mapLegacyPluginRelToCanonical(relInPluginSlash string) (string, bool) {
	parts := strings.SplitN(relInPluginSlash, "/", 2)
	if len(parts) < 2 {
		return "", false
	}
	switch parts[0] {
	case "skills":
		return path.Join(manifest.SkillsRoot, parts[1]), true
	case "agents":
		if strings.Contains(parts[1], "/") || !strings.HasSuffix(parts[1], ".agent.md") {
			return "", false
		}
		return path.Join(manifest.AgentsRoot, parts[1]), true
	default:
		return "", false
	}
}

// removeEmptyTree walks `root` bottom-up and removes every directory that
// becomes empty. If `root` itself ends up empty, it is removed too.
func removeEmptyTree(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		// Continue on errors (root missing is fine).
		if err != nil {
			return nil
		}
		return nil
	})
	// Two-pass: collect dirs, sort by depth desc, remove if empty.
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(filepath.Separator)) > strings.Count(dirs[j], string(filepath.Separator))
	})
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil || len(entries) > 0 {
			continue
		}
		_ = os.Remove(d)
	}
}

// checkInstallCollisions does a preflight scan over the canonical
// destinations that the incoming plugins want to write. It refuses when
//
//   - two incoming plugins map to the same destination path (the flat
//     project-scope namespace means a same-named skill from two upstream
//     plugins would clobber each other), or
//   - an incoming destination is currently owned by a DIFFERENT locked
//     plugin (would clobber another tracked plugin's file), or
//   - an incoming destination exists on disk but isn't tracked by any
//     locked plugin (would clobber an untracked / hand-installed file)
//     and force=false.
//
// A destination owned by the SAME plugin id (a routine re-install or
// `update`) is always permitted.
func checkInstallCollisions(repoRoot string, plugins []layout.Plugin, lock *manifest.Lock, force bool) error {
	// Build owner map of currently-locked destinations.
	locked := map[string]string{} // canonical path → plugin id
	for _, p := range lock.Plugins {
		for _, f := range p.Files {
			locked[filepath.ToSlash(f.Path)] = p.ID
		}
	}

	// Set of incoming plugin ids (so we can detect intra-batch conflicts).
	incoming := map[string]bool{}
	for _, p := range plugins {
		incoming[p.Name] = true
	}

	seen := map[string]string{} // canonical → first plugin in this batch
	for _, p := range plugins {
		for _, f := range p.Files {
			dest := filepath.ToSlash(f.CanonicalPath)
			if owner, ok := seen[dest]; ok && owner != p.Name {
				return fmt.Errorf("collision: plugins %q and %q both install to %s; rename or exclude one",
					owner, p.Name, dest)
			}
			seen[dest] = p.Name
			if owner, ok := locked[dest]; ok && owner != p.Name {
				return fmt.Errorf("collision: %s is already owned by plugin %q; refusing to overwrite from %q",
					dest, owner, p.Name)
			}
			// Untracked file collision.
			if _, ok := locked[dest]; ok {
				continue // same plugin re-install — OK
			}
			full := filepath.Join(repoRoot, filepath.FromSlash(dest))
			if _, err := os.Stat(full); err == nil && !force {
				return fmt.Errorf("destination %s already exists and is not tracked by gh-copilot-curate; pass --force to overwrite",
					dest)
			}
		}
	}
	return nil
}

// CheckNoLegacyLayout returns a descriptive error if the target repo still
// contains an install from an earlier version of this tool. v0.3.0 moved
// installs from .agent-pack/ (v0.2.x) to .copilot/agent-pack/; v0.4.0
// renamed the tool from gh-agent-pack to gh-copilot-curate and moved the
// tool-state dir from .copilot/agent-pack/ to .copilot/curate/. Running
// v0.4+ against either legacy layout would silently create a parallel
// install and leave AGENTS.md links inconsistent.
//
// Note: the v0.5 → v0.6 layout change (.copilot/plugins/<plugin>/ →
// .agents/skills/ + .github/agents/) is auto-migrated by
// migrateLegacyPluginsLayout and does NOT trigger an error here, because
// installations from v0.5 are common enough that we want a smooth upgrade.
//
// Callers should invoke this from any mutating command (init, add, update,
// remove) before performing IO. Verify/list are intentionally exempt so
// users can still inspect a legacy install.
func CheckNoLegacyLayout(repoRoot string) error {
	// v0.3.x layout (this tool, when it was still called gh-agent-pack).
	v03 := filepath.Join(repoRoot, manifest.LegacyV03ToolStateDir)
	if _, err := os.Stat(v03); err == nil {
		return fmt.Errorf("this repository uses the v0.3 layout at %s/; v0.4 installs to %s/.\n"+
			"The tool was also renamed from gh-agent-pack to gh-copilot-curate.\n"+
			"To migrate, either:\n"+
			"  1. (Recommended for unmerged installs) Delete %s/ and any %s/plugins/ contents, then re-run:\n"+
			"       gh copilot-curate init && gh copilot-curate add <owner/repo>[@ref]\n"+
			"  2. Move %s/manifest.yml to %s, delete the rest of %s/, then run\n"+
			"     `gh copilot-curate add` (no args) to re-resolve the lock.\n"+
			"AGENTS.md and .github/instructions/copilot-curate.instructions.md will be regenerated automatically.",
			manifest.LegacyV03ToolStateDir, manifest.ToolStateDir,
			manifest.LegacyV03ToolStateDir, manifest.PackRoot,
			manifest.LegacyV03ToolStateDir, manifest.ManifestPath, manifest.LegacyV03ToolStateDir)
	}
	// v0.2.x layout.
	v02 := filepath.Join(repoRoot, manifest.LegacyPackRoot)
	if _, err := os.Stat(v02); err == nil {
		return fmt.Errorf("this repository uses the v0.2 layout at %s/; v0.4 installs to %s/.\n"+
			"To migrate, either:\n"+
			"  1. (Recommended for unmerged installs) Delete %s/ and re-run:\n"+
			"       gh copilot-curate init && gh copilot-curate add <owner/repo>[@ref]\n"+
			"  2. Move %s/manifest.yml to %s and delete %s/.\n"+
			"     Then run `gh copilot-curate add` (no args) to re-resolve the lock.\n"+
			"AGENTS.md and .github/instructions/copilot-curate.instructions.md will be regenerated automatically.",
			manifest.LegacyPackRoot, manifest.ToolStateDir,
			manifest.LegacyPackRoot,
			manifest.LegacyPackRoot, manifest.ManifestPath, manifest.LegacyPackRoot)
	}
	return nil
}
