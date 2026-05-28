// Package agents writes the gh-skills managed block into AGENTS.md and
// .github/copilot-instructions.md, and extracts skill summaries from
// installed SKILL.md / .agent.md files.
//
// The managed block is delimited by HTML comment markers so it can be
// rewritten idempotently without touching user-authored content. Anything
// outside the markers is preserved verbatim.
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
	BeginMarker = "<!-- BEGIN gh-skills managed -->"
	EndMarker   = "<!-- END gh-skills managed -->"

	AgentsFile      = "AGENTS.md"
	CopilotInstFile = ".github/copilot-instructions.md"
)

// Entry is one row in the managed block.
type Entry struct {
	Plugin  string // plugin id
	Title   string // short title (filename if no H1 found)
	Summary string // first paragraph, single-line
	Link    string // repo-relative path (e.g. .skills/plugins/x/skills/y/SKILL.md)
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

// WriteManagedBlock rewrites the managed block in the given file (creating
// the file if it does not yet exist). Returns true if the file changed.
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
	// Use a randomized temp name in the same dir so a pre-existing symlink
	// at a predictable path can't be used as a write-redirect.
	tmp, err := os.CreateTemp(filepath.Dir(full), filepath.Base(full)+".tmp-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(updated); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return false, err
	}
	return true, nil
}

// ManagedBlockMatches returns true if the managed block in relPath equals
// what would be written for entries. Used by `verify`.
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

func normalizeEOL(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func bytesEqualEOLAware(a, b []byte) bool {
	return normalizeEOL(string(a)) == normalizeEOL(string(b))
}

func renderManagedBlock(entries []Entry) string {
	// Group by plugin, list skills then agents within each.
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
	b.WriteString(BeginMarker + "\n")
	b.WriteString("## Available skills (managed by gh-skills — do not edit by hand)\n\n")
	b.WriteString("Run `gh skills list` to see installed plugins; run `gh skills update` to refresh.\n\n")
	if len(plugins) == 0 {
		b.WriteString("_No plugins installed yet. Add one with `gh skills add owner/repo`._\n")
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
			switch mode {
			case "link":
				fmt.Fprintf(&b, "- [%s](%s) — _%s_\n", e.Title, e.Link, kind)
			case "inline":
				summary := strings.TrimSpace(e.Summary)
				if summary == "" {
					summary = "_no description_"
				}
				fmt.Fprintf(&b, "- [%s](%s) — _%s_ — %s\n", e.Title, e.Link, kind, summary)
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
				fmt.Fprintf(&b, "- [%s](%s) — _%s_ — %s\n", e.Title, e.Link, kind, summary)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(EndMarker + "\n")
	return b.String()
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
