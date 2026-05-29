package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesSkillsScaffold(t *testing.T) {
	root := t.TempDir()
	// Need a marker so resolveRoot succeeds.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	cmd := NewRootCmd("test")
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"init", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".skills", "manifest.yml")); err != nil {
		t.Errorf("manifest not created: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "BEGIN gh-skill-pack managed") {
		t.Errorf("missing managed block:\n%s", body)
	}
}

func TestInitIsNonDestructive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := []byte("# my manifest\nplugins: []\n")
	if err := os.MkdirAll(filepath.Join(root, ".skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".skills", "manifest.yml"), pre, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"init", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, ".skills", "manifest.yml"))
	if !bytes.Equal(got, pre) {
		t.Errorf("manifest was rewritten:\n%s", got)
	}
}

func TestListEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	cmd := NewRootCmd("test")
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"list", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No plugins installed") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestVerifyCleanWhenNoPlugins(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	cmd := NewRootCmd("test")
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"verify", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestAddRejectsBadSpec(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"add", "not-a-spec", "--root", root, "--yes"})
	if err := cmd.Execute(); err == nil {
		t.Errorf("expected error for bad spec")
	}
}
