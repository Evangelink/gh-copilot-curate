package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseSpec(t *testing.T) {
	cases := []struct {
		in        string
		wantOwner string
		wantRepo  string
		wantRef   string
		wantErr   bool
	}{
		{in: "dotnet/skills", wantOwner: "dotnet", wantRepo: "skills"},
		{in: "dotnet/skills@v1.0.0", wantOwner: "dotnet", wantRepo: "skills", wantRef: "v1.0.0"},
		{in: "dotnet/skills@main", wantOwner: "dotnet", wantRepo: "skills", wantRef: "main"},
		{in: "dotnet/skills@release/2025-q4", wantOwner: "dotnet", wantRepo: "skills", wantRef: "release/2025-q4"},
		{in: "dotnet/skills@deadbeefcafe1234567890abcdef1234567890abcd", wantOwner: "dotnet", wantRepo: "skills", wantRef: "deadbeefcafe1234567890abcdef1234567890abcd"},
		{in: "", wantErr: true},
		{in: "no-slash", wantErr: true},
		{in: "owner/repo/extra", wantErr: true},
		{in: "/repo", wantErr: true},
		{in: "owner/", wantErr: true},
		{in: "owner/repo@", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseSpec(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseSpec(%q) = %+v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSpec(%q) error: %v", c.in, err)
			continue
		}
		if got.Owner != c.wantOwner || got.Repo != c.wantRepo || got.Ref != c.wantRef {
			t.Errorf("ParseSpec(%q) = %+v, want owner=%q repo=%q ref=%q", c.in, got, c.wantOwner, c.wantRepo, c.wantRef)
		}
	}
}

func TestSpecString(t *testing.T) {
	if got := (Spec{Owner: "o", Repo: "r"}).String(); got != "o/r" {
		t.Errorf("got %q", got)
	}
	if got := (Spec{Owner: "o", Repo: "r", Ref: "v1"}).String(); got != "o/r@v1" {
		t.Errorf("got %q", got)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	tmp := t.TempDir()
	tgz := makeTarGz(t, []tarEntry{
		{name: "good/file.txt", body: []byte("ok")},
		{name: "../escape.txt", body: []byte("bad")},
	})
	if err := ExtractTarGz(bytes.NewReader(tgz), tmp); err == nil {
		t.Fatalf("expected error on traversal entry")
	}
}

func TestExtractTarGzRejectsAbsolute(t *testing.T) {
	tmp := t.TempDir()
	tgz := makeTarGz(t, []tarEntry{{name: "/etc/passwd", body: []byte("bad")}})
	if err := ExtractTarGz(bytes.NewReader(tgz), tmp); err == nil {
		t.Fatalf("expected error on absolute entry")
	}
}

func TestExtractTarGzRejectsSymlink(t *testing.T) {
	tmp := t.TempDir()
	tgz := makeTarGzWithSymlink(t)
	if err := ExtractTarGz(bytes.NewReader(tgz), tmp); err == nil {
		t.Fatalf("expected error on symlink entry")
	}
}

func TestExtractTarGzRejectsDriveLetter(t *testing.T) {
	// Defends against Windows drive-qualified entries like "C:/evil" that
	// slip past the leading-"/" check. On non-Windows hosts, "C:" is a
	// valid filename component (no volume semantics), so the threat model
	// only applies to Windows and the test is skipped elsewhere.
	if runtime.GOOS != "windows" {
		t.Skip("drive-letter entries only have volume semantics on Windows")
	}
	tmp := t.TempDir()
	tgz := makeTarGz(t, []tarEntry{{name: "C:/evil.txt", body: []byte("bad")}})
	if err := ExtractTarGz(bytes.NewReader(tgz), tmp); err == nil {
		t.Fatalf("expected error on drive-qualified entry")
	}
}

func TestExtractTarGzWritesFiles(t *testing.T) {
	tmp := t.TempDir()
	tgz := makeTarGz(t, []tarEntry{
		{name: "pkg-abc123/README.md", body: []byte("hi")},
		{name: "pkg-abc123/plugins/x/SKILL.md", body: []byte("body")},
	})
	if err := ExtractTarGz(bytes.NewReader(tgz), tmp); err != nil {
		t.Fatal(err)
	}
	root, err := FindExtractedRoot(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(root) != "pkg-abc123" {
		t.Fatalf("unexpected root: %s", root)
	}
	got, err := os.ReadFile(filepath.Join(root, "plugins", "x", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "body" {
		t.Fatalf("got %q", string(got))
	}
}

func TestFindExtractedRootErrorsWhenMultiple(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FindExtractedRoot(tmp); err == nil {
		t.Fatalf("expected error when 2 top-level dirs")
	}
}

type tarEntry struct {
	name string
	body []byte
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
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

func makeTarGzWithSymlink(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "link", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
