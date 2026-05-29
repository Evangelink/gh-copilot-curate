// Package manifest defines the schema and IO for the two manifest files
// that gh-agent-pack writes into a target repository:
//
//   - .agent-pack/manifest.yml      — declared intent (hand-curatable)
//   - .agent-pack/manifest.lock.yml — resolved state (generated; do not hand-edit)
//
// The split mirrors the npm/cargo/pip pattern: humans curate intent, the tool
// generates and maintains the lock.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// ManifestPath is the path (relative to the repo root) of the intent file.
	ManifestPath = ".agent-pack/manifest.yml"
	// LockPath is the path (relative to the repo root) of the generated lock.
	LockPath = ".agent-pack/manifest.lock.yml"
	// PackDir is the root directory under which gh-agent-pack writes installed files.
	PackDir = ".agent-pack"
	// ManagedBy is the marker written into the lock file so downstream tools
	// can detect that the directory is managed by gh-agent-pack.
	ManagedBy = "gh-agent-pack"
	// SchemaVersion is the current manifest/lock schema version.
	SchemaVersion = 1
)

// InstallMode controls how a plugin's content is surfaced to AI agents via
// AGENTS.md / copilot-instructions.md.
type InstallMode string

const (
	// ModeSummary writes a one-paragraph summary of each skill plus a link to
	// the full SKILL.md. Default, balances signal vs file size.
	ModeSummary InstallMode = "summary"
	// ModeInline writes the full SKILL.md / agent body into the managed block.
	// Most reliable for cloud-agent pickup; bloats AGENTS.md.
	ModeInline InstallMode = "inline"
	// ModeLink writes only a link list. Smallest footprint; relies on the
	// model traversing the link.
	ModeLink InstallMode = "link"
)

// ValidModes returns the supported install modes (for flag validation / help).
func ValidModes() []InstallMode { return []InstallMode{ModeSummary, ModeInline, ModeLink} }

// Manifest is the hand-curatable intent file.
type Manifest struct {
	Version int      `yaml:"version"`
	Plugins []Plugin `yaml:"plugins,omitempty"`
}

// Plugin is one declared plugin entry in the intent manifest.
type Plugin struct {
	ID      string         `yaml:"id"`
	Source  string         `yaml:"source"`            // owner/repo
	Ref     string         `yaml:"ref,omitempty"`     // tag, branch, or SHA
	Include []string       `yaml:"include,omitempty"` // glob list of upstream paths
	Install *InstallConfig `yaml:"install,omitempty"`
}

// InstallConfig is per-plugin install configuration.
type InstallConfig struct {
	Mode InstallMode `yaml:"mode,omitempty"`
}

// Lock is the generated lock file mirroring the resolved state of every
// plugin declared in the manifest.
type Lock struct {
	Version      int          `yaml:"version"`
	ManagedBy    string       `yaml:"managedBy"`
	ToolVersion  string       `yaml:"toolVersion"`
	GeneratedAt  time.Time    `yaml:"generatedAt"`
	ManifestHash string       `yaml:"manifestHash"`
	Plugins      []LockPlugin `yaml:"plugins,omitempty"`
}

// LockPlugin is the lock entry for one installed plugin.
type LockPlugin struct {
	ID           string        `yaml:"id"`
	Source       LockSource    `yaml:"source"`
	RequestedRef string        `yaml:"requestedRef,omitempty"`
	ResolvedRef  string        `yaml:"resolvedRef"`
	Layout       string        `yaml:"layout"`
	Install      InstallConfig `yaml:"install"`
	Files        []LockFile    `yaml:"files,omitempty"`
}

// LockSource is the resolved provenance of a plugin.
type LockSource struct {
	Type  string `yaml:"type"` // "github"
	Host  string `yaml:"host"` // "github.com"
	Owner string `yaml:"owner"`
	Repo  string `yaml:"repo"`
}

// LockFile is one installed file's record.
type LockFile struct {
	Path         string `yaml:"path"`         // local path relative to repo root
	UpstreamPath string `yaml:"upstreamPath"` // path inside the upstream repo at ResolvedRef
	UpstreamHash string `yaml:"upstreamHash"` // sha256 of the upstream blob at ResolvedRef
	LocalHash    string `yaml:"localHash"`    // sha256 of the file as written by gh-agent-pack
	Mode         string `yaml:"mode"`         // POSIX-style octal, e.g. "0644"
}

// LoadManifest reads .agent-pack/manifest.yml under repoRoot. Returns an empty
// manifest (not an error) when the file does not exist, so callers can treat
// "no skills installed" as a normal state.
func LoadManifest(repoRoot string) (*Manifest, error) {
	path := filepath.Join(repoRoot, ManifestPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Manifest{Version: SchemaVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	m := &Manifest{}
	if err := yaml.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version == 0 {
		m.Version = SchemaVersion
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// SaveManifest writes the manifest atomically. Creates parent dirs as needed.
func SaveManifest(repoRoot string, m *Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	path := filepath.Join(repoRoot, ManifestPath)
	return writeYAMLAtomic(path, m, manifestHeader)
}

// LoadLock reads .agent-pack/manifest.lock.yml. Returns a zero-valued lock when
// the file does not exist.
func LoadLock(repoRoot string) (*Lock, error) {
	path := filepath.Join(repoRoot, LockPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Lock{Version: SchemaVersion, ManagedBy: ManagedBy}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock: %w", err)
	}
	l := &Lock{}
	if err := yaml.Unmarshal(data, l); err != nil {
		return nil, fmt.Errorf("parse lock: %w", err)
	}
	return l, nil
}

// SaveLock writes the lock atomically with stable ordering and a header.
func SaveLock(repoRoot string, l *Lock) error {
	l.ManagedBy = ManagedBy
	if l.Version == 0 {
		l.Version = SchemaVersion
	}
	if l.GeneratedAt.IsZero() {
		l.GeneratedAt = time.Now().UTC()
	}
	sort.SliceStable(l.Plugins, func(i, j int) bool { return l.Plugins[i].ID < l.Plugins[j].ID })
	for i := range l.Plugins {
		sort.SliceStable(l.Plugins[i].Files, func(a, b int) bool {
			return l.Plugins[i].Files[a].Path < l.Plugins[i].Files[b].Path
		})
	}
	path := filepath.Join(repoRoot, LockPath)
	return writeYAMLAtomic(path, l, lockHeader)
}

// Validate enforces the minimum invariants on the intent manifest.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("nil manifest")
	}
	if m.Version != 0 && m.Version != SchemaVersion {
		return fmt.Errorf("unsupported manifest schema version %d (expected %d)", m.Version, SchemaVersion)
	}
	seen := map[string]bool{}
	for i, p := range m.Plugins {
		if p.ID == "" {
			return fmt.Errorf("plugin %d: id is required", i)
		}
		if seen[p.ID] {
			return fmt.Errorf("plugin %q: duplicate id", p.ID)
		}
		seen[p.ID] = true
		if p.Source == "" {
			return fmt.Errorf("plugin %q: source is required (owner/repo)", p.ID)
		}
		if p.Install != nil && p.Install.Mode != "" && !IsValidMode(p.Install.Mode) {
			return fmt.Errorf("plugin %q: invalid install.mode %q (want summary|inline|link)", p.ID, p.Install.Mode)
		}
	}
	return nil
}

// FindPlugin returns the plugin with the given id, or nil.
func (m *Manifest) FindPlugin(id string) *Plugin {
	for i := range m.Plugins {
		if m.Plugins[i].ID == id {
			return &m.Plugins[i]
		}
	}
	return nil
}

// Upsert inserts or replaces a plugin entry in place, preserving original
// order when replacing.
func (m *Manifest) Upsert(p Plugin) {
	for i := range m.Plugins {
		if m.Plugins[i].ID == p.ID {
			m.Plugins[i] = p
			return
		}
	}
	m.Plugins = append(m.Plugins, p)
}

// Remove drops the plugin with the given id. Returns true if removed.
func (m *Manifest) Remove(id string) bool {
	for i := range m.Plugins {
		if m.Plugins[i].ID == id {
			m.Plugins = append(m.Plugins[:i], m.Plugins[i+1:]...)
			return true
		}
	}
	return false
}

// FindPlugin returns the lock entry for a plugin id, or nil.
func (l *Lock) FindPlugin(id string) *LockPlugin {
	for i := range l.Plugins {
		if l.Plugins[i].ID == id {
			return &l.Plugins[i]
		}
	}
	return nil
}

// Upsert inserts or replaces a lock entry.
func (l *Lock) Upsert(p LockPlugin) {
	for i := range l.Plugins {
		if l.Plugins[i].ID == p.ID {
			l.Plugins[i] = p
			return
		}
	}
	l.Plugins = append(l.Plugins, p)
}

// Remove drops the lock entry for a plugin id.
func (l *Lock) Remove(id string) bool {
	for i := range l.Plugins {
		if l.Plugins[i].ID == id {
			l.Plugins = append(l.Plugins[:i], l.Plugins[i+1:]...)
			return true
		}
	}
	return false
}

// HashManifest returns a stable sha256 of the manifest's canonical YAML
// representation. Used to populate Lock.ManifestHash and to detect
// "manifest changed but lock not regenerated" drift.
func HashManifest(m *Manifest) (string, error) {
	buf, err := yaml.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// HashBytes returns the sha256 of arbitrary bytes in the same "sha256:HEX"
// format used throughout the lock file.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IsValidMode reports whether m is one of the supported install modes.
func IsValidMode(m InstallMode) bool {
	for _, v := range ValidModes() {
		if v == m {
			return true
		}
	}
	return false
}

const manifestHeader = `# .agent-pack/manifest.yml — declared skill/agent intent.
# Edit this file by hand to declare which plugins this repo wants.
# Run "gh agent-pack sync" (or any "gh agent-pack" mutating command) to apply.
`

const lockHeader = `# .agent-pack/manifest.lock.yml — generated by gh-agent-pack.
# Do not edit by hand: re-run "gh agent-pack" commands to regenerate.
`

// writeYAMLAtomic marshals v with a header comment and writes path atomically.
func writeYAMLAtomic(path string, v any, header string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(header); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
