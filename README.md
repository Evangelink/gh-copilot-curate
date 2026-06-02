# gh-agent-pack

> **A repo-scoped agent pack manager.** Installs whole *plugin bundles*
> (SKILL.md files + `.agent.md` sub-agents + supporting scripts) from a
> source repo into your target repo, tracks them in a committed manifest,
> and surfaces them in `AGENTS.md` / `.github/copilot-instructions.md` so
> every contributor — and the GitHub.com Copilot **cloud agent** — sees
> what your repo declares.

> Modeled on [`gh aw`](https://github.com/githubnext/gh-aw)'s lifecycle.
> Ships precompiled binaries via `gh extension install`. v0.3.0 introduces
> a **breaking layout change**: installs now live under `.copilot/` (was
> `.agent-pack/` in v0.2.x).

## v0.3.0 — breaking change: install path is now `.copilot/`

Starting with **v0.3.0**, `gh-agent-pack` installs plugin content under
`.copilot/` instead of `.agent-pack/`, aligning with Copilot CLI's own
`~/.copilot/installed-plugins/...` convention. Layout:

| Old (v0.2.x) | New (v0.3.0+) |
|---|---|
| `.agent-pack/manifest.yml` | `.copilot/agent-pack/manifest.yml` |
| `.agent-pack/manifest.lock.yml` | `.copilot/agent-pack/manifest.lock.yml` |
| `.agent-pack/plugins/<plugin>/...` | `.copilot/plugins/<plugin>/...` |
| `.agent-pack/.gitattributes` | `.copilot/.gitattributes` |

**Migrating from v0.2.x**: any mutating command (`init`, `add`, `update`,
`remove`) will detect a legacy `.agent-pack/` directory and refuse to run
with an actionable error. Two paths forward:

- **Unmerged install** (simplest): `git rm -rf .agent-pack` (or
  `Remove-Item .agent-pack -Recurse -Force` on Windows), then
  `gh agent-pack init && gh agent-pack add ...` again. The AGENTS.md
  managed block will be regenerated with the new paths.
- **Already-committed install**: `git mv .agent-pack/manifest.yml
  .copilot/agent-pack/manifest.yml` (create the parent dir first), delete
  the rest of `.agent-pack/`, and re-run `gh agent-pack add` with no
  arguments to regenerate the lock and re-extract plugin content under
  `.copilot/plugins/`.

## TL;DR — which tool should I use?

| You want to… | Use |
|---|---|
| Install one or two SKILL.md skills onto your local machine | **Built-in [`gh skill`](https://cli.github.com/manual/gh_skill) (GitHub CLI 2.92+, ⚠ preview).** Official, supports per-agent dirs, has search and a public catalogue. |
| Commit a SKILL.md skill into your repo so every contributor and `@copilot` see it | Either tool works. `gh-agent-pack` adds the AGENTS.md managed block and a manifest. |
| Install a whole *plugin bundle* (skills + agents + scripts) from a repo like [`dotnet/skills`](https://github.com/dotnet/skills) | **`gh-agent-pack`.** The built-in installs one SKILL.md at a time and ignores `.agent.md` files. |
| Install / update `.agent.md` sub-agent definitions | **`gh-agent-pack`.** The built-in has no concept of agents. |
| Drift-detect your repo's pack state in CI | **`gh-agent-pack verify`.** |

`gh-agent-pack` complements `gh skill` — it does not replace it for
single-skill installs. The two can coexist in the same repo (different
on-disk dirs: `.copilot/plugins/` vs `.agents/skills/`).

> ⚠ **`gh skill` is officially in preview.** Every subcommand is
> labelled `(preview)` and its help text states it is "subject to
> change without notice." Pin a `gh` CLI version if you wire it into
> CI.

## Why this exists

- `/plugin install` and `gh skill install` are **user-local** by default
  (`gh skill install --scope=project` does commit into the project, but
  only one SKILL.md at a time).
- The GitHub.com Copilot **cloud agent** runs in Actions and **only sees
  what's committed** — primarily `AGENTS.md` and
  `.github/copilot-instructions.md`. User-installed skills and locally
  configured agents are invisible to it.
- `dotnet/skills`-style sources ship **plugins** — coherent bundles of
  skills + `.agent.md` agents + scripts under a single plugin id — and
  no tool currently installs the bundle as a unit.

`gh-agent-pack` fills those gaps: one command installs a whole plugin
into your repo, the manifest tells everyone what's pinned, and the
managed block in `AGENTS.md` surfaces the bundle so any agent that reads
that file (Copilot cloud agent, Claude Code, Cursor in repo mode, etc.)
has at least pointers to it.

> ⚠ **Honest caveat about cloud-agent discovery.** We *write to*
> `AGENTS.md` and `.github/copilot-instructions.md` — the files
> Copilot's cloud agent reads when working on an issue. Whether the
> cloud agent then *follows the links* into `.copilot/plugins/...SKILL.md` /
> `.agent.md` is up to the agent's behavior, not something this tool
> can guarantee. Use `--mode inline` for skills the agent **must** see
> in full (it embeds the SKILL.md body directly in `AGENTS.md` so no
> link-following is required).

## Prerequisites

- [GitHub CLI](https://cli.github.com/) installed and authenticated
  (`gh auth login`). `gh agent-pack add` / `update` use your
  `gh auth token` to fetch source-repo tarballs from GitHub. Private
  repos require an account with read access.
- `git` on PATH (`gh agent-pack init` and other commands prefer
  `git rev-parse --show-toplevel` for repo-root detection, with a
  marker-walk fallback).

## Install

```sh
gh extension install Evangelink/gh-agent-pack
```

Upgrade later with:

```sh
gh extension upgrade agent-pack
```

## Quickstart

```sh
# 1. Scaffold .copilot/plugins/, .copilot/agent-pack/manifest.yml,
#    AGENTS.md managed block, and .copilot/.gitattributes
#    (all non-destructive).
gh agent-pack init

# 2. Install a plugin from a source repo, pinned to a release tag.
#    This pulls the entire plugin: skills + .agent.md files + scripts.
gh agent-pack add dotnet/skills@v1.0.0 --plugin dotnet-test --yes

# 3. Check for drift later.
gh agent-pack verify

# 4. Update everything to the latest pinned refs.
gh agent-pack update

# 5. List what's installed.
gh agent-pack list

# 6. Remove a plugin.
gh agent-pack remove dotnet-test
```

Commit the resulting `.copilot/`, `AGENTS.md`, and (if present)
`.github/copilot-instructions.md`. From that point, every contributor
and the cloud agent picks them up automatically.

## Commands

| Command | What it does |
|---|---|
| `gh agent-pack init` | Create `.copilot/` scaffolding and AGENTS.md managed block. Non-destructive. |
| `gh agent-pack add <spec> [flags]` | Install a plugin from a source repo. |
| `gh agent-pack list` | List installed plugins from the lock file. |
| `gh agent-pack verify` | Check file hashes + managed-block freshness + manifest/lock consistency. |
| `gh agent-pack remove <plugin>` | Remove a plugin. Refuses on local edits unless `--force`. |
| `gh agent-pack update [plugin]…` | Re-resolve refs and re-install. Refuses on drift unless `--force`. |

### `gh agent-pack add`

```
gh agent-pack add <owner>/<repo>[@<ref>] [flags]

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
gh agent-pack add dotnet/skills@v1.0.0 --plugin dotnet-test --yes

# Install only the build-perf skill from the dotnet-msbuild plugin.
gh agent-pack add dotnet/skills@main --plugin dotnet-msbuild \
  --include "skills/build-perf/**"

# Pin and inline a critical skill so its SKILL.md body lands in AGENTS.md.
gh agent-pack add my-org/my-skills@v2 --mode inline --yes
```

## Layout

```
.copilot/
  .gitattributes            # `* text eol=lf` — keeps hashes stable across OSes
                            # (covers both agent-pack/ and plugins/ subtrees)
  agent-pack/
    manifest.yml            # intent — hand-editable
    manifest.lock.yml       # generated — do not edit
  plugins/
    <plugin>/
      skills/<skill>/SKILL.md
      skills/<skill>/scripts/...
      agents/<agent>.agent.md
AGENTS.md
  # ...your existing content...
  <!-- BEGIN gh-agent-pack managed -->
  ## Available skills (managed by gh-agent-pack — do not edit by hand)
  ...summary entries...
  <!-- END gh-agent-pack managed -->
.github/copilot-instructions.md   # optional — same managed block
```

## Manifest schema

### `.copilot/agent-pack/manifest.yml` (intent)

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

### `.copilot/agent-pack/manifest.lock.yml` (generated)

```yaml
version: 1
managedBy: gh-agent-pack
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
      - path: .copilot/plugins/dotnet-test/skills/run-tests/SKILL.md
        upstreamPath: plugins/dotnet-test/skills/run-tests/SKILL.md
        upstreamHash: sha256:...
        localHash: sha256:...
        mode: "0644"
```

## Cloud-agent integration modes

The Copilot **cloud agent** (and any contributor) reads `AGENTS.md` and
`.github/copilot-instructions.md`. `gh-agent-pack` writes a single
managed block into those files based on the install mode:

| Mode | What lands in AGENTS.md | When to use |
|---|---|---|
| `summary` (default) | Per-skill `### name` + 1-line description + repo-relative link to the SKILL.md | Most skills. Keeps AGENTS.md skim-able. Assumes the agent will follow links. |
| `inline` | Full SKILL.md body, fenced | Critical skills the agent **must** read in full. No link-following required. |
| `link` | Just a bullet list of links | Minimal noise; only if you trust the agent to follow links. |

If your cloud agent doesn't reliably follow links into
`.copilot/plugins/...`, prefer `inline` for skills that must always be
applied.

## Security model

- **No script execution.** `gh-agent-pack` never runs anything it installs.
- **Path-traversal rejection.** All entries are validated against the repo root before any IO. Tarball entries with absolute, drive-qualified, or `..` paths are refused, and we verify each resolved target lives under the destination via `filepath.Rel`.
- **Symlinks and hardlinks in tarballs are rejected** to avoid host-symlink path-traversal vectors.
- **Tarball caps.** Extraction is bounded to 5,000 files, 25 MB per file, and 200 MB total to defend against compression bombs.
- **Pin to tags.** `gh-agent-pack` warns when you install from a branch ref (mutable). Prefer `@v1.0.0` or a commit SHA.
- **Lock-file paths are validated** on every read so a malicious checked-in lock cannot redirect writes outside `.copilot/`.
- **Atomic writes** via `os.CreateTemp` in the target directory + rename, so a crashed install never leaves a half-written file in place of a good one.
- **Provenance in lock.** The resolved commit SHA and upstream hash for every file are recorded so `gh agent-pack verify` can detect tampering or drift.

## Comparison with related tooling

| | `/plugin install` | `gh skill install` (built-in 2.92+, ⚠ preview) | `gh agent-pack add` |
|---|---|---|---|
| Default scope | User machine | User machine (`--scope=project` commits to repo) | Repository (always commits) |
| Surfaces in AGENTS.md for the cloud agent | ❌ | ❌ | ✅ |
| Picked up by `@copilot` on github.com | ❌ | ⚠ Only if you also wire AGENTS.md by hand | ✅ |
| Reproducible across contributors | ❌ | ✅ (project scope) | ✅ |
| Single committed manifest / lock | ❌ | ❌ (state lives in per-skill frontmatter) | ✅ |
| Plugin-bundle install (skills + agents + scripts in one go) | ❌ | ❌ (one skill at a time) | ✅ |
| Handles `.agent.md` sub-agent files | ❌ | ❌ | ✅ |
| Drift detection in CI | ❌ | ❌ | ✅ `gh agent-pack verify` |
| Public catalogue / search | ❌ | ✅ | ❌ |
| Preserves plugin namespace on disk | n/a | ❌ (flattens — `dotnet-test/foo` → `.agents/skills/foo/`) | ✅ |

## Development

```sh
git clone https://github.com/Evangelink/gh-agent-pack
cd gh-agent-pack
go build ./...
go test ./...
```

Install your local build into `gh`:

```sh
# produces ./gh-agent-pack (or gh-agent-pack.exe on Windows)
go build -o gh-agent-pack .          # use gh-agent-pack.exe on Windows
gh extension install .
gh agent-pack --help
```

## Roadmap (v0.3+)

- Real 3-way merge on `gh agent-pack update` (fetch BASE by SHA, cache)
- `--scope=user` with host detection (Copilot CLI / Claude / Cursor / VS Code) — interop with built-in `gh skill`'s per-agent dirs
- `agentskills.io` standard layout support
- `gh agent-pack sync` to reconcile from hand-edited manifest
- URL specs (`https://github.com/.../blob/...`) and local specs (`./path`)
- Signature/provenance verification
- Private-repo auth, rate-limit handling
- Partial-failure recovery (transactional add/update)
- Smoke-test harness that validates cloud-agent discovery of summary/inline/link modes against `@copilot` on github.com

## License

MIT — see [LICENSE](LICENSE).
