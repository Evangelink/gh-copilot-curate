package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRootFromSubdir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, found, err := FindRoot(sub)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	gotResolved, _ := filepath.EvalSymlinks(got)
	rootResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != rootResolved {
		t.Errorf("got=%q want=%q", got, root)
	}
}

func TestFindRootNoMarker(t *testing.T) {
	root := t.TempDir()
	got, found, err := FindRoot(root)
	if found || err == nil {
		t.Fatalf("expected not-found, got found=%v err=%v root=%q", found, err, got)
	}
}

func TestMustCleanRelRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := MustCleanRel(root, "../escape.txt"); err == nil {
		t.Errorf("expected escape rejection")
	}
	if _, err := MustCleanRel(root, ".agent-pack/manifest.yml"); err != nil {
		t.Errorf("ok path rejected: %v", err)
	}
}
