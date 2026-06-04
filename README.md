# gh-copilot-curate

> **A repo-scoped Copilot context curator.** Installs whole *plugin bundles*
> (SKILL.md files + `.agent.md` sub-agents + supporting scripts) from a
> source repo into your target repo, tracks them in a committed manifest,
> and surfaces them via the AGENTS.md managed block and a path-specific
> custom-instructions file at
> `.github/instructions/copilot-curate.instructions.md` so every
> contributor — and the GitHub.com Copilot **cloud agent** — sees what
> your repo declares.

> Modeled on [`gh aw`](https://github.com/githubnext/gh-aw)'s lifecycle.
> Ships precompiled binaries via `gh extension install`. **v0.6.0** is a
> **breaking change** that moves installed plugin content out of the
> tool-specific `.copilot/plugins/<plugin>/...` tree and into the
> canonical paths a user would create by hand (`.agents/skills/<skill>/`
> for skills, `.github/agents/<name>.agent.md` for agents). This makes
> installed content auto-discoverable by Copilot CLI, the GitHub.com
> Copilot cloud agent, the built-in `gh skill`, and IDE chats with **no
> indirection layer** — they look like files the user created
> themselves. The migration runs automatically on the next mutating
> command. See migration notes below.

## v0.6.0 — breaking change: canonical user-equivalent install paths

In v0.4–v0.5, plugin content was staged under
`.copilot/plugins/<plugin>/...`. That worked but it required every
agentic surface to know about an extra "managed install" layer to find
the SKILL.md files. v0.6.0 drops that layer:

| Content | v0.5 path | v0.6 path |
|---|---|---|
| Skills | `.copilot/plugins/<plugin>/skills/<skill>/SKILL.md` | `.agents/skills/<skill>/SKILL.md` |
| Skill scripts/refs | `.copilot/plugins/<plugin>/skills/<skill>/scripts/...` | `.agents/skills/<skill>/scripts/...` |
| Agents | `.copilot/plugins/<plugin>/agents/<name>.agent.md` | `.github/agents/<name>.agent.md` |

These are the exact paths a user would create by hand and the paths
`gh skill install --scope=project` writes. Net effect: any surface that
reads `.agents/skills/` or `.github/agents/` directly (Copilot CLI, the
cloud agent, IDE chats, `gh skill list`) now picks up curate-installed
content as if the user had authored it.

**Trade-off — flat project namespace.** Because the canonical paths
strip the `<plugin>` prefix, two upstream plugins that ship a skill
with the same directory name cannot coexist in the same repo. The
preflight collision check refuses the install with a clear error; use
`--include` to scope to one of them.

**Automatic migration**: the next time you run **any** mutating command
(`add`, `update`, `remove`), gh-copilot-curate will:

1. Verify each `.copilot/plugins/...` file still hashes to its
   `LocalHash` in the lock (refuses with a `--force`-able error if any
   file has been hand-edited — see "drift" below).
2. `os.Rename` every file into its v0.6 canonical path. Modes and
   inodes are preserved; only the lock's `Path` field is rewritten.
3. Drop any plugin-metadata orphans (e.g. `plugin.json`) from both the
   lock and disk.
4. Remove the now-empty `.copilot/plugins/` subtree.
5. Refresh the repo-root `.gitattributes` managed block to cover
   `.agents/skills/**` and `.github/agents/**` with `text eol=lf`.
6. Print a one-shot migration notice.

`gh copilot-curate verify` reports the legacy layout as drift
(`LegacyLayoutPending`) until a mutating command runs.

## v0.5.0 — breaking change: instructions moved to a path-specific file

In v0.4.x, `gh-copilot-curate` wrote a managed block into the shared
`.github/copilot-instructions.md`, which forced the tool to coexist with
hand-authored repo-wide instructions in a single file.

In v0.5.0 the managed block is replaced by a **dedicated path-specific
custom-instructions file** owned entirely by the tool:

```
.github/instructions/copilot-curate.instructions.md
```

The file carries a YAML front-matter (`applyTo: "**"`) so GitHub Copilot
treats it as a custom-instructions file applying to every file in the
repo. AGENTS.md continues to carry the same managed block (unchanged
markers, unchanged inventory).

**Surface coverage** (from the
[GitHub Copilot custom-instructions support matrix](https://docs.github.com/en/copilot/reference/custom-instructions-support)):

| Surface | Reads the new file? |
|---|---|
| GitHub.com Copilot **cloud agent** (assigned issues, `@copilot`) | ✅ |
| GitHub.com code review | ✅ |
| Copilot CLI | ✅ |
| VS Code Chat, cloud agent, Visual Studio Chat, JetBrains, Xcode | ✅ |
| GitHub.com **Copilot Chat** | ❌ — falls back to AGENTS.md only |
| Eclipse Chat, VS Code code review | ❌ — falls back to AGENTS.md only |

For surfaces that don't yet support path-specific instructions, the
AGENTS.md managed block still carries the same inventory, so coverage
degrades gracefully.

**Automatic migration**: the next time you run **any** mutating command
(`init`, `add`, `update`, `remove`), gh-copilot-curate will:

1. Write `.github/instructions/copilot-curate.instructions.md` with the
   current inventory and `applyTo: "**"` front-matter.
2. Strip the v0.4 managed block from
   `.github/copilot-instructions.md`. If that file becomes empty (i.e.
   you never added hand-authored content alongside the managed block),
   it is deleted. Otherwise your hand-authored content is preserved
   verbatim.
3. Print a one-shot migration notice.

`gh copilot-curate verify` reports the legacy block as drift until a
mutating command runs.

## v0.4.0 — breaking change: tool renamed to `gh copilot-curate`

The extension was renamed from `gh-agent-pack` → `gh-copilot-curate` to
reflect its broader scope (skills, agents, and — coming soon — prompts and
instructions). The on-disk tool-state dir moved correspondingly:

| Old (v0.3.x) | New (v0.4.0+) |
|---|---|
| `gh agent-pack ...` | `gh copilot-curate ...` |
| `.copilot/agent-pack/manifest.yml` | `.copilot/curate/manifest.yml` |
| `.copilot/agent-pack/manifest.lock.yml` | `.copilot/curate/manifest.lock.yml` |
| `<!-- BEGIN gh-agent-pack managed -->` | `<!-- BEGIN gh-copilot-curate managed -->` |

Plugin content moves to canonical user-equivalent paths
(`.agents/skills/<skill>/` and `.github/agents/<name>.agent.md`) in
v0.6.0 — see the v0.6.0 section above.

**Migrating from v0.3.x**: any mutating command (`init`, `add`, `update`,
`remove`) detects a legacy `.copilot/agent-pack/` directory and refuses
to run with an actionable error. Two paths:

- **Unmerged install** (simplest): `git rm -rf .copilot` (or
  `Remove-Item .copilot -Recurse -Force` on Windows), then
  `gh copilot-curate init && gh copilot-curate add ...` again. The AGENTS.md
  managed block will be regenerated with the new markers.
- **Already-committed install**: `git mv .copilot/agent-pack/manifest.yml
  .copilot/curate/manifest.yml` (create the parent dir first), delete the
  rest of `.copilot/agent-pack/`, and re-run `gh copilot-curate add` with
  no arguments to regenerate the lock under the new path. Then re-install
  the extension as `gh extension remove agent-pack && gh extension install
  Evangelink/gh-copilot-curate`.

## v0.3.0 — breaking change: install path is now `.copilot/`

In **v0.3.0**, installs moved from `.agent-pack/` (v0.2.x) to `.copilot/`
to align with Copilot CLI's own `~/.copilot/installed-plugins/...`
convention. v0.4+ still detects and refuses to run against a stale
`.agent-pack/` layout — follow the same two-path migration above but
delete `.agent-pack/` instead of `.copilot/agent-pack/`.

## TL;DR — which tool should I use?

| You want to… | Use |
|---|---|
| Install one or two SKILL.md skills onto your local machine | **Built-in [`gh skill`](https://cli.github.com/manual/gh_skill) (GitHub CLI 2.92+, ⚠ preview).** Official, supports per-agent dirs, has search and a public catalogue. |
| Commit a SKILL.md skill into your repo so every contributor and `@copilot` see it | Either tool works. `gh-copilot-curate` adds the AGENTS.md managed block and a manifest. |
| Install a whole *plugin bundle* (skills + agents + scripts) from a repo like [`dotnet/skills`](https://github.com/dotnet/skills) | **`gh-copilot-curate`.** The built-in installs one SKILL.md at a time and ignores `.agent.md` files. |
| Install / update `.agent.md` sub-agent definitions | **`gh-copilot-curate`.** The built-in has no concept of agents. |
| Drift-detect your repo's pack state in CI | **`gh-copilot-curate verify`.** |

`gh-copilot-curate` complements `gh skill` — it does not replace it for
single-skill installs. As of v0.6.0 both tools write to the same
on-disk paths (`.agents/skills/` + `.github/agents/`), so a
curate-installed bundle and a `gh skill --scope=project` install
coexist in the same tree.

> ⚠ **`gh skill` is officially in preview.** Every subcommand is
> labelled `(preview)` and its help text states it is "subject to
> change without notice." Pin a `gh` CLI version if you wire it into
> CI.

## Why this exists

- `/plugin install` and `gh skill install` are **user-local** by default
  (`gh skill install --scope=project` does commit into the project, but
  only one SKILL.md at a time).
- The GitHub.com Copilot **cloud agent** runs in Actions and **only sees
  what's committed** — primarily `AGENTS.md` and the path-specific
  custom-instructions files under `.github/instructions/`. User-installed
  skills and locally configured agents are invisible to it.
- `dotnet/skills`-style sources ship **plugins** — coherent bundles of
  skills + `.agent.md` agents + scripts under a single plugin id — and
  no tool currently installs the bundle as a unit.

`gh-copilot-curate` fills those gaps: one command installs a whole plugin
into your repo, the manifest tells everyone what's pinned, and the
managed block in `AGENTS.md` surfaces the bundle so any agent that reads
that file (Copilot cloud agent, Claude Code, Cursor in repo mode, etc.)
has at least pointers to it.

> ⚠ **Honest caveat about cloud-agent discovery.** We *write to*
> `AGENTS.md` and `.github/instructions/copilot-curate.instructions.md`
> — the files Copilot's cloud agent reads when working on an issue.
> Whether the cloud agent then *follows the links* into
> `.agents/skills/...SKILL.md` / `.github/agents/...agent.md` is up to
> the agent's behavior, not something this tool can guarantee. Use
> `--mode inline` for skills the agent **must** see in full (it embeds
> the SKILL.md body directly so no link-following is required).

## Prerequisites

- [GitHub CLI](https://cli.github.com/) installed and authenticated
  (`gh auth login`). `gh copilot-curate add` / `update` use your
  `gh auth token` to fetch source-repo tarballs from GitHub. Private
  repos require an account with read access.
- `git` on PATH (`gh copilot-curate init` and other commands prefer
  `git rev-parse --show-toplevel` for repo-root detection, with a
  marker-walk fallback).

## Install

```sh
gh extension install Evangelink/gh-copilot-curate
```

Upgrade later with:

```sh
gh extension upgrade copilot-curate
```

## Quickstart

```sh
# 1. Scaffold .copilot/curate/, AGENTS.md managed block, and
#    .copilot/.gitattributes. (Plugin content lives at canonical
#    paths under .agents/skills/ + .github/agents/ once you add.)
gh copilot-curate init

# 2. Install a plugin from a source repo, pinned to a release tag.
#    This pulls the entire plugin: skills + .agent.md files + scripts.
gh copilot-curate add dotnet/skills@v1.0.0 --plugin dotnet-test --yes

# 3. Check for drift later.
gh copilot-curate verify

# 4. Update everything to the latest pinned refs.
gh copilot-curate update

# 5. List what's installed.
gh copilot-curate list

# 6. Remove a plugin.
gh copilot-curate remove dotnet-test
```

Commit the resulting `.copilot/`, `AGENTS.md`, and
`.github/instructions/copilot-curate.instructions.md`. From that point,
every contributor and the cloud agent picks them up automatically.

## Commands

| Command | What it does |
|---|---|
| `gh copilot-curate init` | Create `.copilot/` scaffolding and AGENTS.md managed block. Non-destructive. |
| `gh copilot-curate add <spec> [flags]` | Install a plugin from a source repo. |
| `gh copilot-curate list` | List installed plugins from the lock file. |
| `gh copilot-curate verify` | Check file hashes + managed-block freshness + manifest/lock consistency. |
| `gh copilot-curate remove <plugin>` | Remove a plugin. Refuses on local edits unless `--force`. |
| `gh copilot-curate update [plugin]…` | Re-resolve refs and re-install. Refuses on drift unless `--force`. |

### `gh copilot-curate add`

```
gh copilot-curate add <owner>/<repo>[@<ref>] [flags]

  --plugin NAME       Override plugin id (default: derived from repo)
  --include GLOB,...  Only copy files matching these globs (path/Match syntax,
                      e.g. "skills/build-perf/**" or "agents/*")
  --mode MODE         Cloud-agent integration mode: summary (default) | inline | link
  --dry-run           Print what would happen, do nothing
  --yes               Skip confirmation prompt
```

Examples:

```sh
# Install the full dotnet-test plugin (22 skills + 11 agents + scripts).
gh copilot-curate add dotnet/skills@v1.0.0 --plugin dotnet-test --yes

# Install only the build-perf skill from the dotnet-msbuild plugin.
gh copilot-curate add dotnet/skills@main --plugin dotnet-msbuild \
  --include "skills/build-perf/**"

# Pin and inline a critical skill so its SKILL.md body lands in AGENTS.md.
gh copilot-curate add my-org/my-skills@v2 --mode inline --yes
```

## Layout

```
.copilot/
  .gitattributes            # `* text eol=lf` — keeps hashes stable across OSes
                            # for .copilot/curate/ state files
  curate/
    manifest.yml            # intent — hand-editable
    manifest.lock.yml       # generated — do not edit
.agents/skills/             # canonical skills root (user-equivalent path)
  <skill>/SKILL.md          #   <plugin> prefix is intentionally stripped
  <skill>/scripts/...
.github/agents/             # canonical agents root (user-equivalent path)
  <agent>.agent.md
.gitattributes              # repo-root file gets a managed block adding
                            # `.agents/skills/** text eol=lf` and
                            # `.github/agents/** text eol=lf`. User entries
                            # outside the fence are preserved verbatim.
AGENTS.md
  # ...your existing content...
  <!-- BEGIN gh-copilot-curate managed -->
  ## Available skills (managed by gh-copilot-curate — do not edit by hand)
  ...summary entries...
  <!-- END gh-copilot-curate managed -->
.github/instructions/copilot-curate.instructions.md
  # Path-specific custom-instructions file owned wholesale by gh-copilot-curate.
  # Front-matter: applyTo: "**" — applies to every file in the repo.
  # Contains the same inventory as the AGENTS.md managed block, with
  # links rewritten relative to .github/instructions/.
```

## Manifest schema

### `.copilot/curate/manifest.yml` (intent)

```yaml
version: 1
plugins:
  - id: dotnet-test
    source: dotnet/skills
    ref: v1.0.0
    include: ["skills/**", "agents/**"]   # optional — defaults to the whole plugin
    install:
      mode: summary    # summary | inline | link
```

### `.copilot/curate/manifest.lock.yml` (generated)

```yaml
version: 1
managedBy: gh-copilot-curate
toolVersion: 0.3.0
generatedAt: 2026-05-29T10:00:00Z
manifestHash: sha256:...
plugins:
  - id: dotnet-test
    source:
      type: github
      host: github.com
      owner: dotnet
      repo: skills
    requestedRef: v1.0.0
    resolvedRef: a1b2c3d4...
    layout: dotnet-skills
    install:
      scope: repo
      mode: summary
    files:
      - path: .agents/skills/run-tests/SKILL.md
        upstreamPath: plugins/dotnet-test/skills/run-tests/SKILL.md
        upstreamHash: sha256:...
        localHash: sha256:...
        mode: "0644"
```

## Cloud-agent integration modes

The Copilot **cloud agent** (and any contributor) reads `AGENTS.md` and
the path-specific instructions file at
`.github/instructions/copilot-curate.instructions.md`.
`gh-copilot-curate` writes the same managed inventory into both:

- the AGENTS.md managed block (between the `<!-- BEGIN gh-copilot-curate
  managed -->` / `<!-- END gh-copilot-curate managed -->` markers), and
- the path-specific instructions file, which it owns wholesale (no
  manual edits — the file is regenerated on every mutating command).

The render mode controls **what** lands in both files:

| Mode | What lands in AGENTS.md | When to use |
|---|---|---|
| `summary` (default) | Per-skill `### name` + 1-line description + repo-relative link to the SKILL.md | Most skills. Keeps AGENTS.md skim-able. Assumes the agent will follow links. |
| `inline` | Full SKILL.md body, fenced | Critical skills the agent **must** read in full. No link-following required. |
| `link` | Just a bullet list of links | Minimal noise; only if you trust the agent to follow links. |

If your cloud agent doesn't reliably follow links into
`.agents/skills/...`, prefer `inline` for skills that must always be
applied.

## Security model

- **No script execution.** `gh-copilot-curate` never runs anything it installs.
- **Path-traversal rejection.** All entries are validated against the repo root before any IO. Tarball entries with absolute, drive-qualified, or `..` paths are refused, and we verify each resolved target lives under the destination via `filepath.Rel`.
- **Symlinks and hardlinks in tarballs are rejected** to avoid host-symlink path-traversal vectors.
- **Tarball caps.** Extraction is bounded to 5,000 files, 25 MB per file, and 200 MB total to defend against compression bombs.
- **Pin to tags.** `gh-copilot-curate` warns when you install from a branch ref (mutable). Prefer `@v1.0.0` or a commit SHA.
- **Lock-file paths are validated** on every read so a malicious checked-in lock cannot redirect writes outside `.copilot/`.
- **Atomic writes** via `os.CreateTemp` in the target directory + rename, so a crashed install never leaves a half-written file in place of a good one.
- **Provenance in lock.** The resolved commit SHA and upstream hash for every file are recorded so `gh copilot-curate verify` can detect tampering or drift.

## Comparison with related tooling

| | `/plugin install` | `gh skill install` (built-in 2.92+, ⚠ preview) | `gh copilot-curate add` |
|---|---|---|---|
| Default scope | User machine | User machine (`--scope=project` commits to repo) | Repository (always commits) |
| Surfaces in AGENTS.md for the cloud agent | ❌ | ❌ | ✅ |
| Picked up by `@copilot` on github.com | ❌ | ⚠ Only if you also wire AGENTS.md by hand | ✅ |
| Reproducible across contributors | ❌ | ✅ (project scope) | ✅ |
| Single committed manifest / lock | ❌ | ❌ (state lives in per-skill frontmatter) | ✅ |
| Plugin-bundle install (skills + agents + scripts in one go) | ❌ | ❌ (one skill at a time) | ✅ |
| Handles `.agent.md` sub-agent files | ❌ | ❌ | ✅ |
| Drift detection in CI | ❌ | ❌ | ✅ `gh copilot-curate verify` |
| Public catalogue / search | ❌ | ✅ | ❌ |
| Preserves plugin namespace on disk | n/a | ❌ (flattens — `dotnet-test/foo` → `.agents/skills/foo/`) | ✅ |

## Development

```sh
git clone https://github.com/Evangelink/gh-copilot-curate
cd gh-copilot-curate
go build ./...
go test ./...
```

Install your local build into `gh`:

```sh
# produces ./gh-copilot-curate (or gh-copilot-curate.exe on Windows)
go build -o gh-copilot-curate .          # use gh-copilot-curate.exe on Windows
gh extension install .
gh copilot-curate --help
```

## Roadmap (v0.3+)

- Real 3-way merge on `gh copilot-curate update` (fetch BASE by SHA, cache)
- `--scope=user` with host detection (Copilot CLI / Claude / Cursor / VS Code) — interop with built-in `gh skill`'s per-agent dirs
- `agentskills.io` standard layout support
- `gh copilot-curate sync` to reconcile from hand-edited manifest
- URL specs (`https://github.com/.../blob/...`) and local specs (`./path`)
- Signature/provenance verification
- Private-repo auth, rate-limit handling
- Partial-failure recovery (transactional add/update)
- Smoke-test harness that validates cloud-agent discovery of summary/inline/link modes against `@copilot` on github.com

## License

MIT — see [LICENSE](LICENSE).
