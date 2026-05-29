// Package source resolves and fetches plugin content from upstream
// repositories. v1 supports GitHub repos addressed as "owner/repo[@ref]"
// fetched via the GitHub tarball endpoint, authenticated using the token
// from `gh auth token`.
package source

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Spec describes a parsed source reference. v1 only supports
// "owner/repo[@ref]"; finer-grained selection (plugin name, includes) is
// handled by caller flags.
type Spec struct {
	Host  string // always "github.com" in v1
	Owner string
	Repo  string
	Ref   string // tag, branch, or SHA; empty means "default branch"
}

var (
	// ParseSpec rejects anything that does not match this pattern, by design:
	// path-style specs like "owner/repo/plugin/skill" are ambiguous with
	// branches such as "release/x" and are deferred to a future schema.
	specRE = regexp.MustCompile(`^([A-Za-z0-9][\w.-]*)/([A-Za-z0-9][\w.-]*)(?:@([\w./-]+))?$`)
)

// ParseSpec parses an "owner/repo[@ref]" string.
func ParseSpec(raw string) (Spec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Spec{}, errors.New("empty source spec")
	}
	m := specRE.FindStringSubmatch(raw)
	if m == nil {
		return Spec{}, fmt.Errorf("invalid source %q: expected owner/repo[@ref]", raw)
	}
	return Spec{Host: "github.com", Owner: m[1], Repo: m[2], Ref: m[3]}, nil
}

// String returns the canonical form of a Spec.
func (s Spec) String() string {
	out := s.Owner + "/" + s.Repo
	if s.Ref != "" {
		out += "@" + s.Ref
	}
	return out
}

// Fetcher knows how to resolve refs to SHAs and download trees.
type Fetcher interface {
	ResolveRef(ctx context.Context, s Spec) (sha string, err error)
	DownloadTree(ctx context.Context, s Spec, sha string, dest string) error
}

// HTTPFetcher implements Fetcher using GitHub's REST API + tarball endpoint,
// authenticated via gh's stored token.
type HTTPFetcher struct {
	Client    *http.Client
	UserAgent string

	// Token, if non-empty, is sent as Bearer auth. When empty, the fetcher
	// tries `gh auth token` (cached on first use).
	Token string
}

// NewHTTPFetcher returns a fetcher with sensible defaults.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{
		Client:    &http.Client{Timeout: 60 * time.Second},
		UserAgent: "gh-agent-pack",
	}
}

func (f *HTTPFetcher) token() (string, error) {
	if f.Token != "" {
		return f.Token, nil
	}
	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("get gh auth token (is gh installed and authenticated?): %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", errors.New("gh auth token returned empty value")
	}
	f.Token = tok
	return tok, nil
}

// ResolveRef returns the commit SHA for s.Ref (or default branch if empty).
func (f *HTTPFetcher) ResolveRef(ctx context.Context, s Spec) (string, error) {
	ref := s.Ref
	if ref == "" {
		ref = "HEAD"
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", s.Owner, s.Repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	tok, err := f.token()
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github.sha")
	req.Header.Set("User-Agent", f.UserAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("ref %q not found in %s/%s", ref, s.Owner, s.Repo)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("resolve ref: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(body))
	// Some servers may not honour Accept; try JSON fallback.
	if !looksLikeSHA(sha) {
		var v struct {
			SHA string `json:"sha"`
		}
		if jerr := json.Unmarshal(body, &v); jerr == nil && looksLikeSHA(v.SHA) {
			sha = v.SHA
		} else {
			return "", fmt.Errorf("unexpected response resolving ref: %q", sha)
		}
	}
	return sha, nil
}

// DownloadTree fetches the tarball for sha and extracts it into dest. The
// returned directory contains a single top-level folder named
// "<owner>-<repo>-<shortsha>" (GitHub convention); callers should descend
// into it before treating files as upstream paths.
//
// Path-traversal entries (containing "..") and absolute paths are rejected.
func (f *HTTPFetcher) DownloadTree(ctx context.Context, s Spec, sha string, dest string) error {
	if !looksLikeSHA(sha) {
		return fmt.Errorf("DownloadTree: not a sha: %q", sha)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/tarball/%s", s.Owner, s.Repo, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	tok, err := f.token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", f.UserAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("tarball fetch: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return ExtractTarGz(resp.Body, dest)
}

// Extraction safety caps. These are conservative defaults guarding against
// hostile or accidentally enormous source repos. Adjust here if real-world
// plugin collections start bumping into the limits.
const (
	maxTarFiles     = 5000
	maxTarFileBytes = 25 * 1024 * 1024  // 25 MB per file
	maxTarTotalBytes = 200 * 1024 * 1024 // 200 MB total
)

// ExtractTarGz extracts a gzipped tarball to dest, rejecting unsafe entries
// (absolute paths, ".." entries, symlinks/hardlinks) and enforcing size caps
// to bound disk/CPU exposure to a malicious source repo.
// Exported for testability.
func ExtractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var (
		fileCount  int
		totalBytes int64
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		clean := path.Clean(hdr.Name)
		if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || strings.Contains(clean, "..") {
			return fmt.Errorf("unsafe tar entry: %q", hdr.Name)
		}
		// Reject Windows-style absolute paths like "C:/evil" that survive the
		// slash check above. filepath.VolumeName recognises both drive-letter
		// ("C:") and UNC ("\\\\server\\share") roots after FromSlash.
		osPath := filepath.FromSlash(clean)
		if filepath.IsAbs(osPath) || filepath.VolumeName(osPath) != "" {
			return fmt.Errorf("unsafe tar entry (absolute or volume-qualified): %q", hdr.Name)
		}
		target := filepath.Join(dest, osPath)
		// Belt-and-braces: ensure the resolved target really lives under dest
		// after Clean — defends against any platform-specific quirk we missed.
		absDest, err := filepath.Abs(dest)
		if err != nil {
			return err
		}
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(absDest, absTarget)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return fmt.Errorf("unsafe tar entry (escapes destination): %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			fileCount++
			if fileCount > maxTarFiles {
				return fmt.Errorf("tarball exceeds %d files (refusing for safety)", maxTarFiles)
			}
			if hdr.Size > maxTarFileBytes {
				return fmt.Errorf("tar entry %q exceeds %d bytes (refusing for safety)", hdr.Name, maxTarFileBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			// LimitReader is a defence in depth against headers that lie about Size.
			n, err := io.Copy(f, io.LimitReader(tr, maxTarFileBytes+1))
			if err != nil {
				f.Close()
				return err
			}
			if n > maxTarFileBytes {
				f.Close()
				return fmt.Errorf("tar entry %q exceeds %d bytes during read", hdr.Name, maxTarFileBytes)
			}
			totalBytes += n
			if totalBytes > maxTarTotalBytes {
				f.Close()
				return fmt.Errorf("tarball exceeds %d bytes total (refusing for safety)", maxTarTotalBytes)
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Reject symlinks: skills should not depend on host symlink semantics,
			// and they can be used for path-traversal.
			return fmt.Errorf("symlink entry not allowed: %q", hdr.Name)
		default:
			// Skip other entry types (devices, fifos, etc.).
		}
	}
}

// FindExtractedRoot returns the single top-level directory inside extractDir
// (GitHub tarballs always contain exactly one). Returns an error otherwise.
func FindExtractedRoot(extractDir string) (string, error) {
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("expected exactly 1 top-level dir in tarball, got %d", len(dirs))
	}
	return filepath.Join(extractDir, dirs[0]), nil
}

func looksLikeSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
