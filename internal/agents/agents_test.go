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
		{Plugin: "p1", Title: "build-perf", Summary: "Find slow targets.", Link: ".copilot/plugins/p1/skills/build-perf/SKILL.md"},
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
	if !strings.Contains(s, "[build-perf](.copilot/plugins/p1/skills/build-perf/SKILL.md)") {
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
		Link: ".copilot/plugins/p/skills/x/SKILL.md", Mode: "link",
	}}
	if _, err := WriteManagedBlock(root, AgentsFile, entries); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, AgentsFile))
	s := string(b)
	if strings.Contains(s, "should not appear") {
		t.Errorf("link mode leaked summary:\n%s", s)
	}
	if !strings.Contains(s, "[T](.copilot/plugins/p/skills/x/SKILL.md)") {
		t.Errorf("link missing:\n%s", s)
	}
}

func TestRenderManagedBlockInlineMode(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, ".copilot", "plugins", "p", "skills", "x", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# T\n\nFull body content here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{
		Plugin: "p", Title: "T", Summary: "short",
		Link:     ".copilot/plugins/p/skills/x/SKILL.md",
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

func TestWriteInstructionsFileCreatesFileWithFrontmatter(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{
		{Plugin: "p1", Title: "skill-a", Summary: "desc", Link: ".copilot/plugins/p1/skills/a/SKILL.md"},
	}
	changed, err := WriteInstructionsFile(root, entries)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(InstructionsFile)))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "---\napplyTo: \"**\"\n---\n") {
		t.Fatalf("frontmatter missing or malformed:\n%s", s)
	}
	if strings.Contains(s, BeginMarker) || strings.Contains(s, EndMarker) {
		t.Errorf("instructions file must not carry fence markers:\n%s", s)
	}
	if !strings.Contains(s, "## Available skills") {
		t.Errorf("heading missing:\n%s", s)
	}
}

func TestWriteInstructionsFilePrefixesRelativeLinks(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{
		{Plugin: "p1", Title: "skill-a", Summary: "d", Link: ".copilot/plugins/p1/skills/a/SKILL.md"},
		// Absolute URLs and root-anchored paths stay unchanged regardless of file location.
		{Plugin: "p2", Title: "external", Summary: "d", Link: "https://example.com/doc"},
		{Plugin: "p2", Title: "rooted", Summary: "d", Link: "/docs/agent.md"},
	}
	if _, err := WriteInstructionsFile(root, entries); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(InstructionsFile)))
	s := string(b)
	if !strings.Contains(s, "(../../.copilot/plugins/p1/skills/a/SKILL.md)") {
		t.Errorf("expected relative link prefixed with ../../ for file two levels deep:\n%s", s)
	}
	if !strings.Contains(s, "(https://example.com/doc)") {
		t.Errorf("absolute URL must not be prefixed:\n%s", s)
	}
	if !strings.Contains(s, "(/docs/agent.md)") {
		t.Errorf("root-anchored path must not be prefixed:\n%s", s)
	}
}

func TestWriteInstructionsFileIdempotent(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{{Plugin: "p", Title: "t", Summary: "s", Link: "l"}}
	if _, err := WriteInstructionsFile(root, entries); err != nil {
		t.Fatal(err)
	}
	changed, err := WriteInstructionsFile(root, entries)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("expected no change on identical second write")
	}
}

func TestInstructionsFileMatchesMissingFile(t *testing.T) {
	root := t.TempDir()
	// Empty inventory + missing file → match (we haven't bootstrapped yet).
	ok, err := InstructionsFileMatches(root, nil)
	if err != nil || !ok {
		t.Fatalf("expected match for empty entries on missing file, got ok=%v err=%v", ok, err)
	}
	// Non-empty inventory + missing file → mismatch (file should exist).
	ok, err = InstructionsFileMatches(root, []Entry{{Plugin: "p", Title: "t", Link: "l"}})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("expected mismatch for non-empty entries on missing file")
	}
}

func TestInstructionsFileMatchesAfterWrite(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{{Plugin: "p", Title: "t", Summary: "s", Link: "l"}}
	if _, err := WriteInstructionsFile(root, entries); err != nil {
		t.Fatal(err)
	}
	ok, err := InstructionsFileMatches(root, entries)
	if err != nil || !ok {
		t.Fatalf("matches=%v err=%v", ok, err)
	}
	ok, _ = InstructionsFileMatches(root, []Entry{{Plugin: "different", Title: "x", Link: "l"}})
	if ok {
		t.Errorf("expected mismatch when entries differ")
	}
}

func TestCleanLegacyCopilotInstructionsBlock_NoFile(t *testing.T) {
	root := t.TempDir()
	cleaned, deleted, err := CleanLegacyCopilotInstructionsBlock(root)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned || deleted {
		t.Errorf("expected no-op when file is missing, got cleaned=%v deleted=%v", cleaned, deleted)
	}
}

func TestCleanLegacyCopilotInstructionsBlock_NoMarkers(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(LegacyCopilotInstFile))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := "# Hand-authored instructions\n\nThis is mine.\n"
	if err := os.WriteFile(full, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	cleaned, deleted, err := CleanLegacyCopilotInstructionsBlock(root)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned || deleted {
		t.Errorf("expected no-op when no markers present, got cleaned=%v deleted=%v", cleaned, deleted)
	}
	b, _ := os.ReadFile(full)
	if string(b) != pre {
		t.Errorf("file mutated despite no markers:\n%s", string(b))
	}
}

func TestCleanLegacyCopilotInstructionsBlock_OnlyBlockDeletesFile(t *testing.T) {
	root := t.TempDir()
	// Simulate what v0.4 wrote into a previously-empty file: just the
	// managed block (with surrounding newlines from replaceOrAppendBlock).
	if _, err := WriteManagedBlock(root, LegacyCopilotInstFile,
		[]Entry{{Plugin: "p", Title: "t", Summary: "s", Link: "l"}}); err != nil {
		t.Fatal(err)
	}
	cleaned, deleted, err := CleanLegacyCopilotInstructionsBlock(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cleaned || !deleted {
		t.Fatalf("expected cleaned=true deleted=true, got cleaned=%v deleted=%v", cleaned, deleted)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(LegacyCopilotInstFile))); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted, stat err=%v", err)
	}
}

func TestCleanLegacyCopilotInstructionsBlock_PreservesHandAuthoredContent(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(LegacyCopilotInstFile))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	// Hand-authored content above the managed block.
	pre := "# My repo-wide instructions\n\nUse tabs, not spaces.\n"
	if err := os.WriteFile(full, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteManagedBlock(root, LegacyCopilotInstFile,
		[]Entry{{Plugin: "p", Title: "t", Summary: "s", Link: "l"}}); err != nil {
		t.Fatal(err)
	}
	cleaned, deleted, err := CleanLegacyCopilotInstructionsBlock(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Errorf("expected cleaned=true")
	}
	if deleted {
		t.Errorf("expected deleted=false because hand-authored content remains")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "Use tabs, not spaces.") {
		t.Errorf("hand-authored content lost:\n%s", s)
	}
	if strings.Contains(s, BeginMarker) || strings.Contains(s, EndMarker) {
		t.Errorf("markers should be removed:\n%s", s)
	}
}

func TestCleanLegacyCopilotInstructionsBlock_Idempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteManagedBlock(root, LegacyCopilotInstFile,
		[]Entry{{Plugin: "p", Title: "t", Link: "l"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CleanLegacyCopilotInstructionsBlock(root); err != nil {
		t.Fatal(err)
	}
	// Second run: file is gone, nothing to do.
	cleaned, deleted, err := CleanLegacyCopilotInstructionsBlock(root)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned || deleted {
		t.Errorf("expected idempotent no-op on second call, got cleaned=%v deleted=%v", cleaned, deleted)
	}
}
