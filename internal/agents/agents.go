// Package agents writes the gh-copilot-curate managed content into two
// places, and extracts skill summaries from installed SKILL.md / .agent.md
// files:
//
//   - AGENTS.md (at repo root) — fence-delimited managed block that coexists
//     with hand-authored agent guidance.
//   - .github/instructions/copilot-curate.instructions.md — full file owned
//     entirely by gh-copilot-curate, carrying the same skill inventory with
//     a YAML front-matter (`applyTo: "**"`) so GitHub Copilot picks it up as
//     a path-specific custom-instructions file.
//
// The AGENTS.md managed block is delimited by HTML comment markers so it
// can be rewritten idempotently without touching user-authored content;
// anything outside the markers is preserved verbatim. The instructions
// file is rewritten wholesale.
//
// Versions ≤ v0.4.x wrote a managed block into .github/copilot-instructions.md
// (LegacyCopilotInstFile). v0.5+ migrates away from that file because it is
// shared with hand-authored repo-wide instructions; the named path-specific
// file gives the tool exclusive ownership.
package agents

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	BeginMarker = "<!-- BEGIN gh-copilot-curate managed -->"
	EndMarker   = "<!-- END gh-copilot-curate managed -->"

	AgentsFile = "AGENTS.md"
	// InstructionsFile is the path-specific custom-instructions file
	// gh-copilot-curate owns wholesale from v0.5+. Lives under
	// .github/instructions/ so it is picked up by Copilot CLI, all cloud
	// agents, and code review (per the GitHub support matrix).
	InstructionsFile = ".github/instructions/copilot-curate.instructions.md"
	// LegacyCopilotInstFile is the repo-wide instructions file v0.4.x and
	// earlier wrote a managed block into. v0.5+ mutating commands remove
	// the managed block (and the file itself if it is otherwise empty) so
	// the new InstructionsFile is the only place curate-managed content
	// lives outside AGENTS.md.
	LegacyCopilotInstFile = ".github/copilot-instructions.md"
)

// Entry is one row in the managed block.
type Entry struct {
	Plugin  string // plugin id
	Title   string // short title (filename if no H1 found)
	Summary string // first paragraph, single-line
	Link    string // repo-relative path (e.g. .agents/skills/y/SKILL.md or .github/agents/x.agent.md)
	IsAgent bool   // distinguishes agents from skills in the rendered list
	// Mode controls how the entry is rendered in the managed block:
	//   "summary" (default) — title + 1-line summary + link
	//   "inline"            — full file body fenced, after the title/summary line
	//   "link"              — just title + link, no description
	Mode string
	// FullPath is the absolute on-disk path used to read the body for
	// inline mode. Empty in summary/link modes.
	FullPath string
}

// WriteManagedBlock rewrites the AGENTS.md managed block in the given file
// (creating the file if it does not yet exist). Returns true if the file
// changed. The block is fence-delimited so it coexists with user content
// outside the markers.
func WriteManagedBlock(repoRoot, relPath string, entries []Entry) (changed bool, err error) {
	full := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return false, err
	}
	var existing []byte
	if b, rerr := os.ReadFile(full); rerr == nil {
		existing = b
	} else if !os.IsNotExist(rerr) {
		return false, rerr
	}

	block := renderManagedBlock(entries)
	updated := replaceOrAppendBlock(existing, block)
	if bytesEqualEOLAware(existing, updated) {
		return false, nil
	}
	return true, writeFileAtomic(full, updated)
}

// WriteInstructionsFile rewrites .github/instructions/copilot-curate.instructions.md
// wholesale. The file always carries the YAML front-matter `applyTo: "**"` so
// GitHub Copilot treats it as a path-specific custom-instructions file
// applying to every file in the repo. Returns true if the file changed.
//
// Unlike AGENTS.md this file is owned entirely by gh-copilot-curate: there
// are no fence markers and any hand edits are overwritten on the next
// mutating command.
func WriteInstructionsFile(repoRoot string, entries []Entry) (changed bool, err error) {
	full := filepath.Join(repoRoot, filepath.FromSlash(InstructionsFile))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return false, err
	}
	want := []byte(renderInstructionsFile(entries))
	existing, rerr := os.ReadFile(full)
	if rerr != nil && !os.IsNotExist(rerr) {
		return false, rerr
	}
	if rerr == nil && bytesEqualEOLAware(existing, want) {
		return false, nil
	}
	return true, writeFileAtomic(full, want)
}

// CleanLegacyCopilotInstructionsBlock removes the gh-copilot-curate managed
// block (with its fence markers) from .github/copilot-instructions.md, if
// present. This is the one-shot v0.4 → v0.5 migration: v0.4.x wrote a
// managed block here, v0.5+ writes a separate path-specific file instead.
//
// If the file is empty after the block is removed, the file is deleted. If
// the file contains hand-authored content outside the markers, that content
// is preserved and the file is rewritten without the managed block.
//
// Returns (cleaned, deleted, err) where cleaned reports that a managed
// block was actually removed and deleted reports that the file itself was
// removed. Both false means the file either did not exist or had no
// managed block — in both cases there is nothing to migrate.
func CleanLegacyCopilotInstructionsBlock(repoRoot string) (cleaned bool, deleted bool, err error) {
	full := filepath.Join(repoRoot, filepath.FromSlash(LegacyCopilotInstFile))
	existing, rerr := os.ReadFile(full)
	if os.IsNotExist(rerr) {
		return false, false, nil
	}
	if rerr != nil {
		return false, false, rerr
	}
	if !bytes.Contains(existing, []byte(BeginMarker)) || !bytes.Contains(existing, []byte(EndMarker)) {
		return false, false, nil
	}
	stripped := removeManagedBlock(existing)
	if strings.TrimSpace(string(stripped)) == "" {
		if err := os.Remove(full); err != nil {
			return true, false, err
		}
		return true, true, nil
	}
	if bytesEqualEOLAware(existing, stripped) {
		// Sanity guard: markers were present but removeManagedBlock made
		// no change. Treat as no-op rather than infinite-loop risk.
		return false, false, nil
	}
	if err := writeFileAtomic(full, stripped); err != nil {
		return false, false, err
	}
	return true, false, nil
}

// writeFileAtomic writes data to full atomically (CreateTemp + Rename) with
// a randomized temp name in the same directory so a pre-existing symlink at
// a predictable path can't be used as a write-redirect.
func writeFileAtomic(full string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(full), filepath.Base(full)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, full)
}

// ManagedBlockMatches returns true if the managed block in relPath equals
// what would be written for entries. Used by `verify` for AGENTS.md.
//
// Comparison normalises line endings: a file checked out with CRLF (e.g. via
// core.autocrlf on Windows) is treated as equivalent to its LF form, so we
// don't report spurious drift on cross-platform clones.
func ManagedBlockMatches(repoRoot, relPath string, entries []Entry) (bool, error) {
	full := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	existing, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return len(entries) == 0, nil
		}
		return false, err
	}
	want := renderManagedBlock(entries)
	got := extractBlock(existing)
	return normalizeEOL(strings.TrimSpace(got)) == normalizeEOL(strings.TrimSpace(want)), nil
}

// InstructionsFileMatches returns true if .github/instructions/copilot-curate.instructions.md
// equals what WriteInstructionsFile would produce for entries. Used by `verify`.
//
// Semantics on a missing file:
//   - len(entries) == 0 → match (we haven't written it yet, nothing to drift)
//   - len(entries) > 0  → mismatch (file should exist; report drift)
//
// This mirrors the bootstrap policy in skills.Operations: the instructions
// file is created lazily on the first mutating command that produces a
// non-empty inventory, not on `init`.
func InstructionsFileMatches(repoRoot string, entries []Entry) (bool, error) {
	full := filepath.Join(repoRoot, filepath.FromSlash(InstructionsFile))
	existing, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return len(entries) == 0, nil
		}
		return false, err
	}
	want := renderInstructionsFile(entries)
	return normalizeEOL(string(existing)) == normalizeEOL(want), nil
}

func normalizeEOL(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func bytesEqualEOLAware(a, b []byte) bool {
	return normalizeEOL(string(a)) == normalizeEOL(string(b))
}

func renderManagedBlock(entries []Entry) string {
	var b strings.Builder
	b.WriteString(BeginMarker + "\n")
	b.WriteString(renderInventoryBody(entries, ""))
	b.WriteString(EndMarker + "\n")
	return b.String()
}

// renderInstructionsFile renders the full body of the path-specific
// instructions file at .github/instructions/copilot-curate.instructions.md.
//
// Layout:
//   - YAML front-matter with applyTo: "**" so Copilot applies the
//     instructions to every file in the repo.
//   - Same inventory body as the AGENTS.md managed block, but with link
//     paths prefixed by "../../" so Markdown relative-path resolution
//     reaches the .copilot/ tree from .github/instructions/.
//
// The file has no fence markers — gh-copilot-curate owns it wholesale.
func renderInstructionsFile(entries []Entry) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("applyTo: \"**\"\n")
	b.WriteString("---\n")
	b.WriteString(renderInventoryBody(entries, "../../"))
	return b.String()
}

// renderInventoryBody emits the H2 heading and per-plugin sections shared
// by AGENTS.md (no link prefix; file lives at repo root) and the path-
// specific instructions file (linkPrefix "../../"; file is two levels deep).
//
// linkPrefix is prepended to every entry.Link that is a relative repo path
// (i.e. does not start with a scheme or "/"); absolute URLs and root-anchored
// paths are emitted as-is.
func renderInventoryBody(entries []Entry, linkPrefix string) string {
	byPlugin := map[string][]Entry{}
	for _, e := range entries {
		byPlugin[e.Plugin] = append(byPlugin[e.Plugin], e)
	}
	var plugins []string
	for k := range byPlugin {
		plugins = append(plugins, k)
	}
	sort.Strings(plugins)

	var b strings.Builder
	b.WriteString("## Available skills (managed by gh-copilot-curate — do not edit by hand)\n\n")
	b.WriteString("Run `gh copilot-curate list` to see installed plugins; run `gh copilot-curate update` to refresh.\n\n")
	if len(plugins) == 0 {
		b.WriteString("_No plugins installed yet. Add one with `gh copilot-curate add owner/repo`._\n")
	}
	for _, p := range plugins {
		items := byPlugin[p]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].IsAgent != items[j].IsAgent {
				return !items[i].IsAgent // skills first, agents last
			}
			return items[i].Title < items[j].Title
		})
		fmt.Fprintf(&b, "### %s\n\n", p)
		for _, e := range items {
			kind := "skill"
			if e.IsAgent {
				kind = "agent"
			}
			mode := e.Mode
			if mode == "" {
				mode = "summary"
			}
			link := prefixLink(e.Link, linkPrefix)
			switch mode {
			case "link":
				fmt.Fprintf(&b, "- [%s](%s) — _%s_\n", e.Title, link, kind)
			case "inline":
				summary := strings.TrimSpace(e.Summary)
				if summary == "" {
					summary = "_no description_"
				}
				fmt.Fprintf(&b, "- [%s](%s) — _%s_ — %s\n", e.Title, link, kind, summary)
				body := readBodyForInline(e.FullPath)
				if body != "" {
					b.WriteString("\n  <details><summary>Full content</summary>\n\n")
					b.WriteString("  ```markdown\n")
					for _, line := range strings.Split(body, "\n") {
						b.WriteString("  ")
						b.WriteString(line)
						b.WriteString("\n")
					}
					b.WriteString("  ```\n\n  </details>\n")
				}
			default: // summary
				summary := strings.TrimSpace(e.Summary)
				if summary == "" {
					summary = "_no description_"
				}
				fmt.Fprintf(&b, "- [%s](%s) — _%s_ — %s\n", e.Title, link, kind, summary)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// prefixLink prepends prefix to relative repo paths only. Absolute URLs
// (anything with a "://" scheme) and root-anchored paths ("/...") are
// emitted unchanged so external links keep working from a deeper file.
func prefixLink(link, prefix string) string {
	if prefix == "" || link == "" {
		return link
	}
	if strings.Contains(link, "://") || strings.HasPrefix(link, "/") {
		return link
	}
	return prefix + link
}

// replaceOrAppendBlock writes the managed block into doc, replacing any
// existing block. If no block exists yet, appends with a leading blank
// line. The block is delimited by BeginMarker and EndMarker.
func replaceOrAppendBlock(doc []byte, block string) []byte {
	begin := bytes.Index(doc, []byte(BeginMarker))
	end := bytes.Index(doc, []byte(EndMarker))
	if begin >= 0 && end > begin {
		// Replace [begin, end+len(EndMarker)+optional newline].
		stop := end + len(EndMarker)
		if stop < len(doc) && doc[stop] == '\n' {
			stop++
		}
		// Strip the block's trailing newline so we don't accumulate blanks.
		newBlock := strings.TrimRight(block, "\n") + "\n"
		var out bytes.Buffer
		out.Write(doc[:begin])
		out.WriteString(newBlock)
		out.Write(doc[stop:])
		return out.Bytes()
	}
	// Append; ensure exactly one blank line between existing content and the
	// block.
	var out bytes.Buffer
	if len(doc) > 0 {
		out.Write(bytes.TrimRight(doc, "\n"))
		out.WriteString("\n\n")
	}
	out.WriteString(strings.TrimRight(block, "\n") + "\n")
	return out.Bytes()
}

// removeManagedBlock strips the gh-copilot-curate managed block (markers
// included, plus the trailing newline) from doc, preserving everything
// outside the markers. If no block is present doc is returned unchanged.
// Used by the v0.4 → v0.5 legacy migration.
func removeManagedBlock(doc []byte) []byte {
	begin := bytes.Index(doc, []byte(BeginMarker))
	end := bytes.Index(doc, []byte(EndMarker))
	if begin < 0 || end <= begin {
		return doc
	}
	stop := end + len(EndMarker)
	if stop < len(doc) && doc[stop] == '\n' {
		stop++
	}
	// Also trim a single leading blank line we inserted in replaceOrAppendBlock
	// so we don't leave an orphan blank where the block used to be.
	cut := begin
	if cut >= 2 && doc[cut-1] == '\n' && doc[cut-2] == '\n' {
		cut--
	}
	var out bytes.Buffer
	out.Write(doc[:cut])
	out.Write(doc[stop:])
	return out.Bytes()
}

func extractBlock(doc []byte) string {
	begin := bytes.Index(doc, []byte(BeginMarker))
	end := bytes.Index(doc, []byte(EndMarker))
	if begin < 0 || end <= begin {
		return ""
	}
	return string(doc[begin : end+len(EndMarker)])
}

// readBodyForInline reads the file at absPath for embedding in the managed
// block. Failures return "" so a transient read error doesn't pollute the
// block; verify will surface the issue separately. Trailing whitespace is
// trimmed for stable rendering.
func readBodyForInline(absPath string) string {
	if absPath == "" {
		return ""
	}
	b, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n\r\t ")
}

// SummarizeFile returns (title, summary) for a SKILL.md or *.agent.md file.
// Title is the first H1 if any, else the basename without extension. Summary
// is the first non-empty, non-heading paragraph, flattened to a single line.
func SummarizeFile(absPath string) (title, summary string, err error) {
	body, err := os.ReadFile(absPath)
	if err != nil {
		return "", "", err
	}
	return SummarizeBody(filepath.Base(absPath), body)
}

// SummarizeBody is the IO-free core of SummarizeFile.
func SummarizeBody(basename string, body []byte) (title, summary string, err error) {
	body = stripFrontMatter(body)
	scan := bufio.NewScanner(bytes.NewReader(body))
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var paraLines []string
	inPara := false
	for scan.Scan() {
		line := scan.Text()
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "# ") && title == "":
			title = strings.TrimSpace(strings.TrimPrefix(trim, "# "))
		case strings.HasPrefix(trim, "#"):
			// any heading — paragraph boundary
			if inPara {
				goto done
			}
		case trim == "":
			if inPara {
				goto done
			}
		default:
			inPara = true
			paraLines = append(paraLines, trim)
		}
	}
done:
	if err := scan.Err(); err != nil {
		return "", "", err
	}
	if title == "" {
		title = strings.TrimSuffix(basename, path.Ext(basename))
		title = strings.TrimSuffix(title, ".agent")
	}
	summary = collapseSpaces(strings.Join(paraLines, " "))
	return title, summary, nil
}

// stripFrontMatter removes a leading YAML/TOML front-matter block if
// present (delimited by --- or +++ on their own lines).
func stripFrontMatter(body []byte) []byte {
	if len(body) < 4 {
		return body
	}
	delim := ""
	switch {
	case bytes.HasPrefix(body, []byte("---\n")), bytes.HasPrefix(body, []byte("---\r\n")):
		delim = "---"
	case bytes.HasPrefix(body, []byte("+++\n")), bytes.HasPrefix(body, []byte("+++\r\n")):
		delim = "+++"
	default:
		return body
	}
	rest := body[len(delim):]
	// Skip the newline after the opening delimiter.
	rest = bytes.TrimLeft(rest, "\r\n")
	idx := bytes.Index(rest, []byte("\n"+delim))
	if idx < 0 {
		return body
	}
	after := rest[idx+1+len(delim):]
	after = bytes.TrimLeft(after, "\r\n")
	return after
}

func collapseSpaces(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if r == '\t' || r == '\r' || r == '\n' || r == ' ' {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}
