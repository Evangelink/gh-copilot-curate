package layout

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDetectDotnetSkills(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "plugins", "dotnet-msbuild", "skills", "build-perf"))
	mustWrite(t, filepath.Join(root, "plugins", "dotnet-msbuild", "skills", "build-perf", "SKILL.md"), "body")

	kind, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if kind != KindDotnetSkills {
		t.Errorf("got %q want dotnet-skills", kind)
	}
}

func TestDetectHeuristic(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "random", "place"))
	mustWrite(t, filepath.Join(root, "random", "place", "SKILL.md"), "body")

	kind, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if kind != KindHeuristic {
		t.Errorf("got %q want heuristic", kind)
	}
}

func TestDetectUnknown(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "hi")
	if _, err := Detect(root); err == nil {
		t.Fatal("expected error")
	}
}

func TestTranslateDotnetSkillsCopiesWholeSubtree(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "plugins", "dotnet-msbuild")
	mustWrite(t, filepath.Join(plugin, "skills", "build-perf", "SKILL.md"), "body")
	mustWrite(t, filepath.Join(plugin, "skills", "build-perf", "scripts", "run.ps1"), "echo hi")
	mustWrite(t, filepath.Join(plugin, "agents", "msbuild.agent.md"), "agent")
	// noise outside plugins dir should be ignored
	mustWrite(t, filepath.Join(root, "README.md"), "ignored")

	plugins, err := Translate(root, KindDotnetSkills, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].Name != "dotnet-msbuild" {
		t.Fatalf("got %+v", plugins)
	}
	got := canonicalSet(plugins[0].Files)
	want := []string{
		".skills/plugins/dotnet-msbuild/agents/msbuild.agent.md",
		".skills/plugins/dotnet-msbuild/skills/build-perf/SKILL.md",
		".skills/plugins/dotnet-msbuild/skills/build-perf/scripts/run.ps1",
	}
	if !equalSorted(got, want) {
		t.Errorf("files: got %v want %v", got, want)
	}
}

func TestTranslateDotnetSkillsPluginFilter(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "plugins", "a", "skills", "x", "SKILL.md"), "a")
	mustWrite(t, filepath.Join(root, "plugins", "b", "skills", "y", "SKILL.md"), "b")

	plugins, err := Translate(root, KindDotnetSkills, "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].Name != "b" {
		t.Fatalf("got %+v", plugins)
	}

	if _, err := Translate(root, KindDotnetSkills, "missing", nil); err == nil {
		t.Fatalf("expected error for missing plugin filter")
	}
}

func TestTranslateDotnetSkillsIncludes(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "plugins", "p")
	mustWrite(t, filepath.Join(plugin, "skills", "keep", "SKILL.md"), "k")
	mustWrite(t, filepath.Join(plugin, "skills", "drop", "SKILL.md"), "d")

	plugins, err := Translate(root, KindDotnetSkills, "p", []string{"skills/keep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %+v", plugins)
	}
	got := canonicalSet(plugins[0].Files)
	want := []string{".skills/plugins/p/skills/keep/SKILL.md"}
	if !equalSorted(got, want) {
		t.Errorf("files: got %v want %v", got, want)
	}
}

func TestTranslateHeuristic(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "lib", "SKILL.md"), "body")
	mustWrite(t, filepath.Join(root, "lib", "scripts", "ignored.ps1"), "no")
	mustWrite(t, filepath.Join(root, "lib", "agent.agent.md"), "agent")

	plugins, err := Translate(root, KindHeuristic, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %+v", plugins)
	}
	// heuristic groups by top-level dir "lib"
	if plugins[0].Name != "lib" {
		t.Errorf("name: %s", plugins[0].Name)
	}
}

func canonicalSet(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.CanonicalPath)
	}
	sort.Strings(out)
	return out
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustMkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
