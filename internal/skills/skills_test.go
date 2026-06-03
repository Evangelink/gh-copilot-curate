package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Evangelink/gh-copilot-curate/internal/manifest"
	"github.com/Evangelink/gh-copilot-curate/internal/source"
)

// stubFetcher serves a fixed tarball + sha for tests; no network.
type stubFetcher struct {
	sha     string
	tarball []byte
}

func (s *stubFetcher) ResolveRef(ctx context.Context, _ source.Spec) (string, error) {
	return s.sha, nil
}

func (s *stubFetcher) DownloadTree(ctx context.Context, _ source.Spec, _ string, dest string) error {
	return source.ExtractTarGz(bytes.NewReader(s.tarball), dest)
}

func TestAddInstallsDotnetSkillsPlugin(t *testing.T) {
	repoRoot := t.TempDir()
	tar := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tar}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")

	res, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(res.Plugins) != 1 || res.Plugins[0].ID != "dotnet-msbuild" {
		t.Fatalf("unexpected plugins: %+v", res.Plugins)
	}

	// Files written
	skill := filepath.Join(repoRoot, ".agents", "skills", "build-perf", "SKILL.md")
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("expected SKILL.md to be written: %v", err)
	}
	if !strings.Contains(string(body), "Build perf") {
		t.Errorf("unexpected SKILL.md content: %s", body)
	}
	// Agent written under .github/agents/.
	if _, err := os.Stat(filepath.Join(repoRoot, ".github", "agents", "msbuild.agent.md")); err != nil {
		t.Errorf("expected agent file: %v", err)
	}

	// Lock written
	lock, err := manifest.LoadLock(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	lp := lock.FindPlugin("dotnet-msbuild")
	if lp == nil || len(lp.Files) < 2 {
		t.Fatalf("lock missing files: %+v", lp)
	}
	if lp.ResolvedRef == "" {
		t.Errorf("ResolvedRef not set: %+v", lp)
	}

	// AGENTS.md managed block written
	agentsMd, err := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsMd), "BEGIN gh-copilot-curate managed") {
		t.Errorf("AGENTS.md missing managed block:\n%s", agentsMd)
	}
	if !strings.Contains(string(agentsMd), "Build perf") {
		t.Errorf("AGENTS.md missing title:\n%s", agentsMd)
	}
}

func TestAddIsIdempotent(t *testing.T) {
	repoRoot := t.TempDir()
	tar := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tar}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")

	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	// Re-running Add should not fail (same source).
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec}); err != nil {
		t.Fatalf("re-add failed: %v", err)
	}
}

func TestVerifyDetectsDrift(t *testing.T) {
	repoRoot := t.TempDir()
	tar := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tar}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	// Clean verify.
	res, err := ops.Verify(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("expected clean verify, got %+v", res)
	}
	// Modify a file → drift expected.
	skill := filepath.Join(repoRoot, ".agents", "skills", "build-perf", "SKILL.md")
	if err := os.WriteFile(skill, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = ops.Verify(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ModifiedFiles) != 1 {
		t.Errorf("expected 1 modified file, got %+v", res.ModifiedFiles)
	}
}

func TestRemoveRefusesOnDrift(t *testing.T) {
	repoRoot := t.TempDir()
	tar := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tar}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(repoRoot, ".agents", "skills", "build-perf", "SKILL.md")
	if err := os.WriteFile(skill, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Remove(RemoveOptions{RepoRoot: repoRoot, PluginName: "dotnet-msbuild"}); err == nil {
		t.Errorf("expected remove to refuse on drift")
	}
	// With Force, remove should succeed.
	if _, err := ops.Remove(RemoveOptions{RepoRoot: repoRoot, PluginName: "dotnet-msbuild", Force: true}); err != nil {
		t.Fatalf("remove --force: %v", err)
	}
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Errorf("expected file removed: %v", err)
	}
}

func makeDotnetSkillsTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"dotnet-skills-abc/README.md":                                                "Project README\n",
		"dotnet-skills-abc/plugins/dotnet-msbuild/skills/build-perf/SKILL.md":        "# Build perf\n\nFind slow MSBuild targets.\n",
		"dotnet-skills-abc/plugins/dotnet-msbuild/skills/build-perf/scripts/x.ps1":   "echo hi\n",
		"dotnet-skills-abc/plugins/dotnet-msbuild/agents/msbuild.agent.md":           "# MSBuild agent\n\nA helpful build agent.\n",
	}
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seedLegacyV05Install creates the on-disk layout that v0.5 would have
// produced for a single dotnet-skills plugin so we can exercise the
// v0.5 → v0.6 auto-migration path.
func seedLegacyV05Install(t *testing.T, repoRoot string) (skillPath, agentPath string) {
	t.Helper()
	skillPath = filepath.Join(repoRoot, ".copilot", "plugins", "dotnet-msbuild", "skills", "build-perf", "SKILL.md")
	scriptPath := filepath.Join(repoRoot, ".copilot", "plugins", "dotnet-msbuild", "skills", "build-perf", "scripts", "x.ps1")
	agentPath = filepath.Join(repoRoot, ".copilot", "plugins", "dotnet-msbuild", "agents", "msbuild.agent.md")
	for _, p := range []string{skillPath, scriptPath, agentPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	skillBody := "# Build perf\n\nFind slow MSBuild targets.\n"
	scriptBody := "echo hi\n"
	agentBody := "# MSBuild agent\n\nA helpful build agent.\n"
	for p, body := range map[string]string{skillPath: skillBody, scriptPath: scriptBody, agentPath: agentBody} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Write a lock that references the v0.5 paths.
	lock := &manifest.Lock{
		Version:     manifest.SchemaVersion,
		ManagedBy:   "gh-copilot-curate",
		ToolVersion: "0.5.0",
		Plugins: []manifest.LockPlugin{
			{
				ID:           "dotnet-msbuild",
				Source:       manifest.LockSource{Type: "github", Host: "github.com", Owner: "dotnet", Repo: "skills"},
				RequestedRef: "v1.0.0",
				ResolvedRef:  "abc1234567890abcdef1234567890abcdef12345",
				Layout:       "dotnet-skills",
				Install:      manifest.InstallConfig{Mode: manifest.ModeSummary},
				Files: []manifest.LockFile{
					{Path: ".copilot/plugins/dotnet-msbuild/skills/build-perf/SKILL.md",
						UpstreamPath: "plugins/dotnet-msbuild/skills/build-perf/SKILL.md",
						UpstreamHash: hashBytes([]byte(skillBody)),
						LocalHash:    hashBytes([]byte(skillBody)), Mode: "0644"},
					{Path: ".copilot/plugins/dotnet-msbuild/skills/build-perf/scripts/x.ps1",
						UpstreamPath: "plugins/dotnet-msbuild/skills/build-perf/scripts/x.ps1",
						UpstreamHash: hashBytes([]byte(scriptBody)),
						LocalHash:    hashBytes([]byte(scriptBody)), Mode: "0644"},
					{Path: ".copilot/plugins/dotnet-msbuild/agents/msbuild.agent.md",
						UpstreamPath: "plugins/dotnet-msbuild/agents/msbuild.agent.md",
						UpstreamHash: hashBytes([]byte(agentBody)),
						LocalHash:    hashBytes([]byte(agentBody)), Mode: "0644"},
				},
			},
		},
	}
	if err := manifest.SaveLock(repoRoot, lock); err != nil {
		t.Fatal(err)
	}
	return skillPath, agentPath
}

func TestAddMigratesV05LayoutToCanonical(t *testing.T) {
	repoRoot := t.TempDir()
	seedLegacyV05Install(t, repoRoot)
	tarball := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tarball}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")

	res, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !res.LegacyLayoutMigrated {
		t.Errorf("expected LegacyLayoutMigrated=true")
	}
	// Canonical files exist.
	for _, p := range []string{
		filepath.Join(repoRoot, ".agents", "skills", "build-perf", "SKILL.md"),
		filepath.Join(repoRoot, ".agents", "skills", "build-perf", "scripts", "x.ps1"),
		filepath.Join(repoRoot, ".github", "agents", "msbuild.agent.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected canonical path %s: %v", p, err)
		}
	}
	// Legacy tree is gone.
	if _, err := os.Stat(filepath.Join(repoRoot, ".copilot", "plugins")); !os.IsNotExist(err) {
		t.Errorf("expected .copilot/plugins removed, got err=%v", err)
	}
	// Lock entries rewritten to canonical paths.
	lock, err := manifest.LoadLock(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	lp := lock.FindPlugin("dotnet-msbuild")
	if lp == nil {
		t.Fatal("plugin missing from lock")
	}
	for _, f := range lp.Files {
		if strings.HasPrefix(filepath.ToSlash(f.Path), ".copilot/plugins") {
			t.Errorf("lock still references legacy path: %s", f.Path)
		}
	}
}

func TestAddRefusesV05MigrationOnDrift(t *testing.T) {
	repoRoot := t.TempDir()
	skillPath, _ := seedLegacyV05Install(t, repoRoot)
	// Hand-edit the v0.5 file → drift.
	if err := os.WriteFile(skillPath, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tarball := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tarball}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec}); err == nil {
		t.Fatal("expected migration to refuse on drift")
	} else if !strings.Contains(err.Error(), "locally-modified") {
		t.Errorf("unexpected error: %v", err)
	}
	// With --force migration proceeds.
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec, Force: true}); err != nil {
		t.Fatalf("add --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".agents", "skills", "build-perf", "SKILL.md")); err != nil {
		t.Errorf("expected canonical SKILL.md after forced migration: %v", err)
	}
}

func TestAddRefusesCollisionWithUntrackedFile(t *testing.T) {
	repoRoot := t.TempDir()
	// Pre-create an untracked file at a destination the install would touch.
	dest := filepath.Join(repoRoot, ".agents", "skills", "build-perf", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("user-authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tarball := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tarball}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec}); err == nil {
		t.Fatal("expected collision error for untracked file at destination")
	}
	// --force allows overwrite.
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec, Force: true}); err != nil {
		t.Fatalf("add --force: %v", err)
	}
}

func TestRemovePreservesSharedRoots(t *testing.T) {
	repoRoot := t.TempDir()
	tarball := makeDotnetSkillsTarball(t)
	ops := &Operations{Fetcher: &stubFetcher{sha: "abc1234567890abcdef1234567890abcdef12345", tarball: tarball}}
	spec, _ := source.ParseSpec("dotnet/skills@v1.0.0")
	if _, err := ops.Add(context.Background(), AddOptions{RepoRoot: repoRoot, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	// A user-authored sibling file under .agents/skills/ must survive.
	siblingSkillDir := filepath.Join(repoRoot, ".agents", "skills", "my-local-skill")
	if err := os.MkdirAll(siblingSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	siblingSkill := filepath.Join(siblingSkillDir, "SKILL.md")
	if err := os.WriteFile(siblingSkill, []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Remove(RemoveOptions{RepoRoot: repoRoot, PluginName: "dotnet-msbuild"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Shared roots survive.
	if _, err := os.Stat(filepath.Join(repoRoot, ".agents", "skills")); err != nil {
		t.Errorf(".agents/skills should still exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".github", "agents")); err != nil {
		t.Errorf(".github/agents should still exist (managed root): %v", err)
	}
	// User content untouched.
	if _, err := os.Stat(siblingSkill); err != nil {
		t.Errorf("expected user-authored SKILL.md to survive: %v", err)
	}
	// Plugin's own skill dir is gone.
	if _, err := os.Stat(filepath.Join(repoRoot, ".agents", "skills", "build-perf")); !os.IsNotExist(err) {
		t.Errorf("expected build-perf dir removed: %v", err)
	}
}
