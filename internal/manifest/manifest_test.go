package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	want := &Manifest{
		Version: SchemaVersion,
		Plugins: []Plugin{
			{
				ID:      "dotnet-msbuild",
				Source:  "dotnet/skills",
				Ref:     "v1.4.0",
				Include: []string{"skills/build-perf"},
				Install: &InstallConfig{Mode: ModeSummary},
			},
		},
	}
	if err := SaveManifest(tmp, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadManifest(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Plugins) != 1 || got.Plugins[0].ID != "dotnet-msbuild" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Plugins[0].Install == nil || got.Plugins[0].Install.Mode != ModeSummary {
		t.Fatalf("install mode lost: %+v", got.Plugins[0].Install)
	}
}

func TestLoadManifestMissingReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	m, err := LoadManifest(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m == nil || len(m.Plugins) != 0 {
		t.Fatalf("expected empty manifest, got %+v", m)
	}
}

func TestManifestDuplicateIDRejected(t *testing.T) {
	tmp := t.TempDir()
	m := &Manifest{
		Plugins: []Plugin{
			{ID: "a", Source: "o/r"},
			{ID: "a", Source: "o/r"},
		},
	}
	if err := SaveManifest(tmp, m); err == nil {
		t.Fatalf("expected duplicate id rejection")
	}
}

func TestManifestRequiresSource(t *testing.T) {
	tmp := t.TempDir()
	m := &Manifest{Plugins: []Plugin{{ID: "x"}}}
	if err := SaveManifest(tmp, m); err == nil {
		t.Fatalf("expected missing-source rejection")
	}
}

func TestLockRoundTripAndOrdering(t *testing.T) {
	tmp := t.TempDir()
	want := &Lock{
		ToolVersion:  "0.1.0",
		ManifestHash: "sha256:deadbeef",
		Plugins: []LockPlugin{
			{
				ID:           "z-plugin",
				Source:       LockSource{Type: "github", Host: "github.com", Owner: "o", Repo: "r"},
				RequestedRef: "main",
				ResolvedRef:  "abc123",
				Layout:       "dotnet-skills",
				Install:      InstallConfig{Mode: ModeSummary},
				Files: []LockFile{
					{Path: ".skills/plugins/z-plugin/skills/b/SKILL.md", UpstreamPath: "plugins/z-plugin/skills/b/SKILL.md", UpstreamHash: "sha256:b", LocalHash: "sha256:b", Mode: "0644"},
					{Path: ".skills/plugins/z-plugin/skills/a/SKILL.md", UpstreamPath: "plugins/z-plugin/skills/a/SKILL.md", UpstreamHash: "sha256:a", LocalHash: "sha256:a", Mode: "0644"},
				},
			},
			{ID: "a-plugin", Source: LockSource{Type: "github", Host: "github.com", Owner: "o", Repo: "r"}, ResolvedRef: "x", Layout: "dotnet-skills", Install: InstallConfig{Mode: ModeSummary}},
		},
	}
	if err := SaveLock(tmp, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadLock(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Plugins) != 2 || got.Plugins[0].ID != "a-plugin" {
		t.Fatalf("plugins not sorted: %+v", got.Plugins)
	}
	z := got.FindPlugin("z-plugin")
	if z == nil || len(z.Files) != 2 || z.Files[0].Path > z.Files[1].Path {
		t.Fatalf("files not sorted: %+v", z)
	}
	if got.ManagedBy != ManagedBy {
		t.Fatalf("managedBy not stamped: %q", got.ManagedBy)
	}
}

func TestHashManifestStable(t *testing.T) {
	m := &Manifest{Version: SchemaVersion, Plugins: []Plugin{{ID: "x", Source: "o/r"}}}
	h1, err := HashManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("manifest hash unstable: %s vs %s", h1, h2)
	}
}

func TestSaveManifestWritesHeader(t *testing.T) {
	tmp := t.TempDir()
	if err := SaveManifest(tmp, &Manifest{Version: SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(tmp, ManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(body[:1]) != "#" {
		t.Fatalf("expected header comment, got: %q", string(body))
	}
}

func TestUpsertReplacesPreservesOrder(t *testing.T) {
	m := &Manifest{Plugins: []Plugin{{ID: "a", Source: "o/r"}, {ID: "b", Source: "o/r"}}}
	m.Upsert(Plugin{ID: "a", Source: "o/r", Ref: "v2"})
	if m.Plugins[0].Ref != "v2" || m.Plugins[1].ID != "b" {
		t.Fatalf("upsert reordered: %+v", m.Plugins)
	}
	m.Upsert(Plugin{ID: "c", Source: "o/r"})
	if len(m.Plugins) != 3 || m.Plugins[2].ID != "c" {
		t.Fatalf("upsert append failed: %+v", m.Plugins)
	}
}

func TestRemove(t *testing.T) {
	m := &Manifest{Plugins: []Plugin{{ID: "a", Source: "o/r"}, {ID: "b", Source: "o/r"}}}
	if !m.Remove("a") || len(m.Plugins) != 1 || m.Plugins[0].ID != "b" {
		t.Fatalf("remove failed: %+v", m.Plugins)
	}
	if m.Remove("zzz") {
		t.Fatalf("expected false on missing id")
	}
}
