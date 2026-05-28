// Package skills orchestrates plugin install / update / verify / remove
// using the manifest, source, layout, and agents subpackages.
//
// All operations are repo-scoped in v1 (--scope=user is deferred). The
// orchestrator is IO-heavy but kept side-effect-isolated behind the
// Operations type so commands can be unit-tested.
//
// TODO(v1.1): Add/Update perform per-file writes interleaved with manifest
// and lock updates. A failure partway through can leave orphaned files
// under .skills/plugins/<id>/ without a corresponding lock entry. We
// considered staging into a temp tree and committing atomically; for v1 the
// recovery path is "re-run `gh skills add` / `gh skills update` to reach a
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
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Evangelink/gh-skills/internal/agents"
	"github.com/Evangelink/gh-skills/internal/layout"
	"github.com/Evangelink/gh-skills/internal/manifest"
	"github.com/Evangelink/gh-skills/internal/repo"
	"github.com/Evangelink/gh-skills/internal/source"
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
	Plugins        []PluginChange
	Manifest       *manifest.Manifest
	Lock           *manifest.Lock
	AgentsChanged  bool
	CopilotChanged bool
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

	tmp, err := os.MkdirTemp("", "gh-skills-fetch-")
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

	res := &AddResult{Manifest: mf, Lock: lock}

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

	if err := EnsureSkillsGitAttributes(opts.RepoRoot); err != nil {
		return res, fmt.Errorf("write .skills/.gitattributes: %w", err)
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
	Files         []string
	DriftDetected bool
	Manifest      *manifest.Manifest
	Lock          *manifest.Lock
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

	res := &RemoveResult{Manifest: mf, Lock: lock}
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
	pruneEmptyDirs(filepath.Join(opts.RepoRoot, manifest.SkillsDir, "plugins", opts.PluginName))

	mf.Remove(opts.PluginName)
	lock.Remove(opts.PluginName)
	if err := manifest.SaveManifest(opts.RepoRoot, mf); err != nil {
		return res, err
	}
	if err := writeLock(opts.RepoRoot, mf, lock); err != nil {
		return res, err
	}
	if err := rewriteAgentsBlocksAfterChange(opts.RepoRoot, lock); err != nil {
		return res, err
	}
	return res, nil
}

// VerifyResult reports drift between the lock and the on-disk state.
type VerifyResult struct {
	MissingFiles      []string
	ModifiedFiles     []string
	UnknownInLock     []string // files in lock with bad path / unreadable
	ManifestStale     bool
	BlockStale        bool
	CopilotBlockStale bool
}

// OK reports whether the verify result is clean.
func (v VerifyResult) OK() bool {
	return len(v.MissingFiles) == 0 && len(v.ModifiedFiles) == 0 && len(v.UnknownInLock) == 0 && !v.ManifestStale && !v.BlockStale && !v.CopilotBlockStale
}

// Verify checks that every locked file exists with matching hash, that the
// manifest hash matches the lock's, and that AGENTS.md and the Copilot
// instructions managed blocks are current.
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
	// Only check the Copilot instructions block if the file exists. We only
	// create it when something has actually been installed; an init-only
	// repo legitimately won't have one yet.
	copilotFull := filepath.Join(repoRoot, filepath.FromSlash(agents.CopilotInstFile))
	if _, statErr := os.Stat(copilotFull); statErr == nil {
		ok, err := agents.ManagedBlockMatches(repoRoot, agents.CopilotInstFile, entries)
		if err != nil {
			return res, err
		}
		if !ok {
			res.CopilotBlockStale = true
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
	Plugins []PluginChange
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

func rewriteAgentsBlocks(repoRoot string, lock *manifest.Lock, res *AddResult) error {
	entries := BuildEntries(repoRoot, lock)
	a, err := agents.WriteManagedBlock(repoRoot, agents.AgentsFile, entries)
	if err != nil {
		return err
	}
	res.AgentsChanged = a
	c, err := agents.WriteManagedBlock(repoRoot, agents.CopilotInstFile, entries)
	if err != nil {
		return err
	}
	res.CopilotChanged = c
	return nil
}

func rewriteAgentsBlocksAfterChange(repoRoot string, lock *manifest.Lock) error {
	entries := BuildEntries(repoRoot, lock)
	if _, err := agents.WriteManagedBlock(repoRoot, agents.AgentsFile, entries); err != nil {
		return err
	}
	if _, err := agents.WriteManagedBlock(repoRoot, agents.CopilotInstFile, entries); err != nil {
		return err
	}
	return nil
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

// EnsureSkillsGitAttributes writes .skills/.gitattributes with
// `* text eol=lf` so files under .skills/ keep stable byte content across
// clones with core.autocrlf enabled. Without this, localHash comparison in
// verify produces spurious drift after a Windows checkout. Idempotent: the
// file is only written if absent or its content differs.
func EnsureSkillsGitAttributes(repoRoot string) error {
	rel := filepath.Join(manifest.SkillsDir, ".gitattributes")
	full := filepath.Join(repoRoot, rel)
	want := []byte("# managed by gh-skills: keep stable byte content across CRLF/LF checkouts\n* text eol=lf\n")
	existing, err := os.ReadFile(full)
	if err == nil && bytes.Equal(existing, want) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return writeFileAtomic(full, want, 0o644)
}

func pruneEmptyDirs(dir string) {
	for d := dir; d != "" && d != filepath.Dir(d); d = filepath.Dir(d) {
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
