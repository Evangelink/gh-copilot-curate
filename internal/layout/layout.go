// Package layout translates an upstream source tree into the canonical
// gh-skills on-disk format under .skills/plugins/<plugin>/...
//
// v1 supports two layouts:
//
//   - "dotnet-skills": plugins live at plugins/<plugin>/{skills,agents,...}.
//     Detection: presence of a top-level "plugins/" directory containing at
//     least one subdirectory with a "skills/" or "agents/" child.
//   - "heuristic": fall back to walking the tree for SKILL.md and *.agent.md
//     files and grouping by the nearest "plugins/<name>" ancestor, or by
//     repo name if none exists.
//
// Detection order is: explicit override (caller flag) > dotnet-skills >
// heuristic. v1 does not support agentskills.io's standard manifest yet
// (deferred).
package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Kind identifies a detected layout.
type Kind string

const (
	KindUnknown      Kind = ""
	KindDotnetSkills Kind = "dotnet-skills"
	KindHeuristic    Kind = "heuristic"
)

// Plugin is a logical bundle of skills+agents+scripts that gh-skills installs
// as a unit under .skills/plugins/<Name>/.
type Plugin struct {
	Name  string  // canonical plugin id (kebab-case)
	Files []File  // every file to copy
}

// File describes a single file to copy from upstream → canonical path.
type File struct {
	UpstreamPath  string      // relative to the extracted repo root
	CanonicalPath string      // relative to repo root, under .skills/plugins/<plugin>/
	Mode          os.FileMode // file mode bits
}

// Detect inspects an extracted upstream tree and returns the detected layout.
// extractedRoot is the directory returned by source.FindExtractedRoot.
func Detect(extractedRoot string) (Kind, error) {
	info, err := os.Stat(extractedRoot)
	if err != nil {
		return KindUnknown, err
	}
	if !info.IsDir() {
		return KindUnknown, fmt.Errorf("%s is not a directory", extractedRoot)
	}
	if isDotnetSkills(extractedRoot) {
		return KindDotnetSkills, nil
	}
	if hasSkillOrAgentFiles(extractedRoot) {
		return KindHeuristic, nil
	}
	return KindUnknown, errors.New("no skills or agents detected in upstream tree")
}

// Translate enumerates plugins to install from the extracted tree according
// to the given Kind.
//
//   - pluginFilter (if non-empty) restricts to one plugin name.
//   - includes (if non-empty) restricts to files whose UpstreamPath has any
//     of these prefixes. Globs are not yet supported (v1.1).
func Translate(extractedRoot string, kind Kind, pluginFilter string, includes []string) ([]Plugin, error) {
	switch kind {
	case KindDotnetSkills:
		return translateDotnetSkills(extractedRoot, pluginFilter, includes)
	case KindHeuristic:
		return translateHeuristic(extractedRoot, pluginFilter, includes)
	default:
		return nil, fmt.Errorf("unsupported layout: %q", kind)
	}
}

func isDotnetSkills(root string) bool {
	plugins := filepath.Join(root, "plugins")
	entries, err := os.ReadDir(plugins)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pdir := filepath.Join(plugins, e.Name())
		if dirExists(filepath.Join(pdir, "skills")) || dirExists(filepath.Join(pdir, "agents")) {
			return true
		}
	}
	return false
}

func hasSkillOrAgentFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "SKILL.md" || strings.HasSuffix(name, ".agent.md") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func translateDotnetSkills(root, pluginFilter string, includes []string) ([]Plugin, error) {
	pluginsRoot := filepath.Join(root, "plugins")
	entries, err := os.ReadDir(pluginsRoot)
	if err != nil {
		return nil, fmt.Errorf("read plugins dir: %w", err)
	}
	var out []Plugin
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if pluginFilter != "" && name != pluginFilter {
			continue
		}
		pluginDir := filepath.Join(pluginsRoot, name)
		files, err := collectPluginFiles(root, pluginDir, name, includes)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			continue
		}
		out = append(out, Plugin{Name: name, Files: files})
	}
	if pluginFilter != "" && len(out) == 0 {
		return nil, fmt.Errorf("plugin %q not found in upstream tree", pluginFilter)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func translateHeuristic(root, pluginFilter string, includes []string) ([]Plugin, error) {
	// Group files by their nearest "plugins/<name>/" ancestor, or by the
	// upstream repo's top-level folder name if none.
	groups := map[string][]File{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name != "SKILL.md" && !strings.HasSuffix(name, ".agent.md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		relSlash := filepath.ToSlash(rel)
		plugin := pluginFromPath(relSlash)
		if pluginFilter != "" && plugin != pluginFilter {
			return nil
		}
		// Match includes against plugin-relative path (after stripping
		// "plugins/<plugin>/" prefix if present) to match README intent.
		relInPlugin := relSlash
		prefix := "plugins/" + plugin + "/"
		if idx := strings.Index(relSlash, prefix); idx >= 0 {
			relInPlugin = relSlash[idx+len(prefix):]
		}
		if !matchesIncludes(relInPlugin, includes) {
			return nil
		}
		// Heuristic only includes the SKILL.md / .agent.md itself, not
		// adjacent scripts. The dotnet-skills path handles full subtrees.
		canonical := canonicalForHeuristic(plugin, relSlash)
		mode := fileMode(p, 0o644)
		groups[plugin] = append(groups[plugin], File{
			UpstreamPath:  relSlash,
			CanonicalPath: canonical,
			Mode:          mode,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []Plugin
	for name, files := range groups {
		sort.Slice(files, func(i, j int) bool { return files[i].CanonicalPath < files[j].CanonicalPath })
		out = append(out, Plugin{Name: name, Files: files})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// collectPluginFiles walks pluginDir and returns matching files. Include
// patterns are interpreted as PLUGIN-RELATIVE — e.g. "skills/build-perf/**"
// selects files under <plugin>/skills/build-perf, not "plugins/<plugin>/skills/...".
// This matches the documented user intent in the README.
func collectPluginFiles(root, pluginDir, plugin string, includes []string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(pluginDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		relSlash := filepath.ToSlash(rel)
		relInPlugin, err := filepath.Rel(pluginDir, p)
		if err != nil {
			return err
		}
		relInPluginSlash := filepath.ToSlash(relInPlugin)
		if !matchesIncludes(relInPluginSlash, includes) {
			return nil
		}
		canonical := path.Join(".skills", "plugins", plugin, relInPluginSlash)
		files = append(files, File{
			UpstreamPath:  relSlash,
			CanonicalPath: canonical,
			Mode:          fileMode(p, 0o644),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].CanonicalPath < files[j].CanonicalPath })
	return files, nil
}

func pluginFromPath(relSlash string) string {
	parts := strings.Split(relSlash, "/")
	for i, p := range parts {
		if p == "plugins" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	if len(parts) > 0 {
		return parts[0]
	}
	return "default"
}

func canonicalForHeuristic(plugin, relSlash string) string {
	// Strip everything up to and including "plugins/<plugin>/" if present.
	prefix := "plugins/" + plugin + "/"
	if idx := strings.Index(relSlash, prefix); idx >= 0 {
		return path.Join(".skills", "plugins", plugin, relSlash[idx+len(prefix):])
	}
	return path.Join(".skills", "plugins", plugin, relSlash)
}

// matchesIncludes reports whether relSlash is selected by includes. Each
// include is interpreted as:
//
//   - a glob pattern (path.Match syntax) when it contains a glob meta char
//     ('*', '?', '['). The full relSlash and each ancestor directory are
//     tried so "skills/build-perf/**" style intent (handled here as
//     "skills/build-perf/*") matches files several levels down.
//   - otherwise a prefix (literal directory or file path).
func matchesIncludes(relSlash string, includes []string) bool {
	if len(includes) == 0 {
		return true
	}
	for _, inc := range includes {
		inc = strings.TrimSpace(inc)
		if inc == "" {
			continue
		}
		if hasGlobMeta(inc) {
			if globMatches(inc, relSlash) {
				return true
			}
			continue
		}
		inc = strings.TrimSuffix(inc, "/")
		if relSlash == inc || strings.HasPrefix(relSlash, inc+"/") {
			return true
		}
	}
	return false
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// globMatches matches pattern against relSlash and any of its prefixes. This
// lets a user-friendly pattern like "skills/build-perf/*" select every file
// under that directory, not only its immediate children.
func globMatches(pattern, relSlash string) bool {
	if ok, _ := path.Match(pattern, relSlash); ok {
		return true
	}
	parts := strings.Split(relSlash, "/")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		if ok, _ := path.Match(pattern, prefix); ok {
			return true
		}
	}
	return false
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileMode(p string, def os.FileMode) os.FileMode {
	info, err := os.Stat(p)
	if err != nil {
		return def
	}
	return info.Mode().Perm()
}
