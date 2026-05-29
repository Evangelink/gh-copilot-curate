package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteManagedBlockCreatesFile(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{
		{Plugin: "p1", Title: "build-perf", Summary: "Find slow targets.", Link: ".agent-pack/plugins/p1/skills/build-perf/SKILL.md"},
	}
	changed, err := WriteManagedBlock(root, AgentsFile, entries)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, err := os.ReadFile(filepath.Join(root, AgentsFile))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, BeginMarker) || !strings.Contains(s, EndMarker) {
		t.Fatalf("markers missing:\n%s", s)
	}
	if !strings.Contains(s, "[build-perf](.agent-pack/plugins/p1/skills/build-perf/SKILL.md)") {
		t.Errorf("expected link in output:\n%s", s)
	}
}

func TestWriteManagedBlockPreservesUserContent(t *testing.T) {
	root := t.TempDir()
	pre := "# My agents file\n\nUser note.\n"
	if err := os.WriteFile(filepath.Join(root, AgentsFile), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := WriteManagedBlock(root, AgentsFile, []Entry{{Plugin: "p", Title: "t", Link: "l"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, AgentsFile))
	s := string(b)
	if !strings.HasPrefix(s, "# My agents file") {
		t.Errorf("user content lost:\n%s", s)
	}
	if !strings.Contains(s, BeginMarker) {
		t.Errorf("block missing:\n%s", s)
	}
}

func TestWriteManagedBlockIdempotent(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{{Plugin: "p", Title: "t", Summary: "s", Link: "l"}}
	if _, err := WriteManagedBlock(root, AgentsFile, entries); err != nil {
		t.Fatal(err)
	}
	changed, err := WriteManagedBlock(root, AgentsFile, entries)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("expected no change on identical write")
	}
}

func TestWriteManagedBlockReplacesExisting(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteManagedBlock(root, AgentsFile, []Entry{{Plugin: "old", Title: "x", Link: "l"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteManagedBlock(root, AgentsFile, []Entry{{Plugin: "new", Title: "y", Link: "l2"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, AgentsFile))
	s := string(b)
	if strings.Contains(s, "old") {
		t.Errorf("old plugin entry leaked:\n%s", s)
	}
	if !strings.Contains(s, "new") {
		t.Errorf("new plugin missing:\n%s", s)
	}
	if strings.Count(s, BeginMarker) != 1 || strings.Count(s, EndMarker) != 1 {
		t.Errorf("duplicate markers:\n%s", s)
	}
}

func TestManagedBlockMatches(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{{Plugin: "p", Title: "t", Summary: "s", Link: "l"}}
	if _, err := WriteManagedBlock(root, AgentsFile, entries); err != nil {
		t.Fatal(err)
	}
	ok, err := ManagedBlockMatches(root, AgentsFile, entries)
	if err != nil || !ok {
		t.Fatalf("matches=%v err=%v", ok, err)
	}
	ok, _ = ManagedBlockMatches(root, AgentsFile, []Entry{{Plugin: "different", Title: "x", Link: "l"}})
	if ok {
		t.Errorf("expected mismatch")
	}
}

func TestSummarizeBodyExtractsH1AndFirstPara(t *testing.T) {
	body := []byte("# Build perf\n\nFind slow MSBuild targets.\n\nMore detail here.\n")
	title, summary, err := SummarizeBody("SKILL.md", body)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Build perf" {
		t.Errorf("title=%q", title)
	}
	if summary != "Find slow MSBuild targets." {
		t.Errorf("summary=%q", summary)
	}
}

func TestSummarizeBodyFallsBackToFilename(t *testing.T) {
	body := []byte("Just a paragraph.\n")
	title, summary, err := SummarizeBody("my-skill.agent.md", body)
	if err != nil {
		t.Fatal(err)
	}
	if title != "my-skill" {
		t.Errorf("title=%q", title)
	}
	if summary != "Just a paragraph." {
		t.Errorf("summary=%q", summary)
	}
}

func TestSummarizeBodyStripsFrontMatter(t *testing.T) {
	body := []byte("---\nname: x\ndescription: ignored\n---\n# Title\n\nHello world.\n")
	title, summary, err := SummarizeBody("SKILL.md", body)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Title" {
		t.Errorf("title=%q", title)
	}
	if summary != "Hello world." {
		t.Errorf("summary=%q", summary)
	}
}

func TestSummarizeBodyMultiLineParagraph(t *testing.T) {
	body := []byte("# T\n\nLine one\nline two\nline three\n\nLine four.\n")
	_, summary, err := SummarizeBody("SKILL.md", body)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "Line one line two line three" {
		t.Errorf("summary=%q", summary)
	}
}

func TestRenderManagedBlockLinkMode(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{{
		Plugin: "p", Title: "T", Summary: "should not appear",
		Link: ".agent-pack/plugins/p/skills/x/SKILL.md", Mode: "link",
	}}
	if _, err := WriteManagedBlock(root, AgentsFile, entries); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, AgentsFile))
	s := string(b)
	if strings.Contains(s, "should not appear") {
		t.Errorf("link mode leaked summary:\n%s", s)
	}
	if !strings.Contains(s, "[T](.agent-pack/plugins/p/skills/x/SKILL.md)") {
		t.Errorf("link missing:\n%s", s)
	}
}

func TestRenderManagedBlockInlineMode(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, ".agent-pack", "plugins", "p", "skills", "x", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# T\n\nFull body content here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{
		Plugin: "p", Title: "T", Summary: "short",
		Link:     ".agent-pack/plugins/p/skills/x/SKILL.md",
		Mode:     "inline",
		FullPath: skill,
	}}
	if _, err := WriteManagedBlock(root, AgentsFile, entries); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, AgentsFile))
	s := string(b)
	if !strings.Contains(s, "Full body content here.") {
		t.Errorf("inline body missing:\n%s", s)
	}
	if !strings.Contains(s, "<details>") {
		t.Errorf("inline details block missing:\n%s", s)
	}
}
