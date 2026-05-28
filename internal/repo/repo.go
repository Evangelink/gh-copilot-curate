// Package repo provides repo-root discovery and small filesystem helpers
// shared by the CLI commands.
package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindRoot resolves a repo root, preferring `git rev-parse --show-toplevel`
// so that we always install into the actual Git repository root rather than
// the nearest package marker (which can land inside a sub-package of a
// monorepo). Falls back to a marker walk (.skills, AGENTS.md, .git, go.mod,
// package.json) when Git is unavailable or the directory is not in a repo.
// If nothing matches, returns start with an error so callers (notably
// `init`) can choose to proceed with the current directory.
func FindRoot(start string) (root string, found bool, err error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", false, err
	}
	if r, ok := gitToplevel(abs); ok {
		return r, true, nil
	}
	dir := abs
	for {
		for _, marker := range []string{".skills", ".git", "AGENTS.md", "go.mod", "package.json"} {
			if _, statErr := os.Stat(filepath.Join(dir, marker)); statErr == nil {
				return dir, true, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs, false, errors.New("no repository root detected; pass --root or run inside a repo")
		}
		dir = parent
	}
}

// gitToplevel returns the Git repository root for dir, if available.
func gitToplevel(dir string) (string, bool) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", false
	}
	return filepath.Clean(top), true
}

// MustCleanRel returns rel as a forward-slash path under root, returning an
// error if rel escapes root (defense in depth against bad lock entries).
func MustCleanRel(root, rel string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	relBack, err := filepath.Rel(absRoot, absFull)
	if err != nil {
		return "", err
	}
	if relBack == ".." || hasParentPrefix(relBack) {
		return "", fmt.Errorf("path %q escapes repo root", rel)
	}
	return filepath.ToSlash(relBack), nil
}

func hasParentPrefix(p string) bool {
	return len(p) >= 3 && (p[:3] == ".."+string(filepath.Separator) || p[:3] == "../")
}
