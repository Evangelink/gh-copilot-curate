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

	"github.com/Evangelink/gh-agent-pack/internal/manifest"
	"github.com/Evangelink/gh-agent-pack/internal/source"
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
	skill := filepath.Join(repoRoot, ".copilot", "plugins", "dotnet-msbuild", "skills", "build-perf", "SKILL.md")
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("expected SKILL.md to be written: %v", err)
	}
	if !strings.Contains(string(body), "Build perf") {
		t.Errorf("unexpected SKILL.md content: %s", body)
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
	if !strings.Contains(string(agentsMd), "BEGIN gh-agent-pack managed") {
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
	skill := filepath.Join(repoRoot, ".copilot", "plugins", "dotnet-msbuild", "skills", "build-perf", "SKILL.md")
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
	skill := filepath.Join(repoRoot, ".copilot", "plugins", "dotnet-msbuild", "skills", "build-perf", "SKILL.md")
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
