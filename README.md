# gh-skill-pack

A [GitHub CLI](https://cli.github.com/) extension that installs and updates
agent skills (e.g. [Anthropic-style SKILL.md packages](https://github.com/dotnet/skills))
from a source repository into a target repository — committing the files so
every contributor and the GitHub.com Copilot **cloud agent** (assigned
issues, `@copilot` mentions) sees them.

> v1 is **repo-scoped**: it lays skill files into `.skills/` and updates
> `AGENTS.md` / `.github/copilot-instructions.md` so any agent that reads
> these files (Copilot cloud agent, Claude Code, Cursor in repo mode, etc.)
> picks them up. Native installation into Claude.app / Cursor user
> directories is on the roadmap.

> Modeled on [`gh aw`](https://github.com/githubnext/gh-aw)'s lifecycle. Like
> `gh aw`, it ships precompiled binaries via `gh extension install`.

## Why this exists

- `/plugin install` is **user-local**. It can't be enforced for everyone
  cloning a repo.
- The GitHub.com Copilot cloud agent runs in Actions and **only sees what's
  committed** — primarily `AGENTS.md` and `.github/copilot-instructions.md`.
  User-installed plugins are invisible to it.
- `gh-skill-pack` commits skills and agents into your repo so the cloud agent
  and every contributor get them, deterministically and reproducibly.

## Relationship to built-in `gh skill`

GitHub CLI 2.92+ ships a built-in `gh skill` / `gh skills` command (preview)
that installs/updates/searches/publishes agent skills across 40+ hosts. If
you only need _your machine_ to install some skills into `.agents/skills/`,
use that — it's the official path.

`gh-skill-pack` is complementary and focuses on **committed, team-wide,
manifest-driven** installs:

- A single `.skills/manifest.yml` you commit to declare which packs are
  installed and pinned — like `package.json` for skills.
- An AGENTS.md / `.github/copilot-instructions.md` managed block so the
  **GitHub.com Copilot cloud agent** (and humans) discover skills via the
  files agents actually read. The built-in does not write AGENTS.md.
- `gh skill-pack verify` for drift detection in CI.
- Layout adapters for non-standard source repos (e.g. dotnet/skills with
  its `plugins.yml`).

You can use both together: the built-in for personal/global installs, this
extension for what your repository commits.

## Prerequisites

- [GitHub CLI](https://cli.github.com/) installed and authenticated (`gh auth login`). `gh skill-pack add` / `update` use your `gh auth token` to fetch source-repo tarballs from GitHub. Private repos require an account with read access.
- `git` on PATH (`gh skill-pack init` and other commands prefer `git rev-parse --show-toplevel` for repo-root detection, with a marker-walk fallback).

## Install

```sh
gh extension install Evangelink/gh-skill-pack
```

Upgrade later with:

```sh
gh extension upgrade skill-pack
```

## Quickstart

```sh
# 1. Scaffold .skills/, .skills/manifest.yml, AGENTS.md managed block,
#    and .skills/.gitattributes (non-destructive).
gh skill-pack init

# 2. Install a plugin from a source repo, pinned to a release tag.
gh skill-pack add dotnet/skills@v1.4.0 --yes

# 3. Check for drift later.
gh skill-pack verify

# 4. Update everything to the latest pinned refs.
gh skill-pack update

# 5. List what's installed.
gh skill-pack list

# 6. Remove a plugin.
gh skill-pack remove dotnet-msbuild
```

Commit the resulting `.skills/`, `AGENTS.md`, and (if present)
`.github/copilot-instructions.md`. From that point, every contributor and the
cloud agent picks them up automatically.

## Commands

| Command | What it does |
|---|---|
| `gh skill-pack init` | Create `.skills/` scaffolding and AGENTS.md managed block. Non-destructive. |
| `gh skill-pack add <spec> [flags]` | Install a plugin from a source repo. |
| `gh skill-pack list` | List installed plugins from the lock file. |
| `gh skill-pack verify` | Check file hashes + managed-block freshness + manifest/lock consistency. |
| `gh skill-pack remove <plugin>` | Remove a plugin. Refuses on local edits unless `--force`. |
| `gh skill-pack update [plugin]…` | Re-resolve refs and re-install. Refuses on drift unless `--force`. |

### `gh skill-pack add`

```
gh skill-pack add <owner>/<repo>[@<ref>] [flags]

  --plugin NAME       Override plugin id (default: derived from repo)
  --include GLOB,...  Only copy files matching these globs (path/Match syntax,
                      e.g. "skills/build-perf/**" or "agents/*")
  --mode MODE         Cloud-agent integration mode: summary (default) | inline | link
  --dry-run           Print what would happen, do nothing
  --yes               Skip confirmation prompt
```

Examples:

```sh
gh skill-pack add dotnet/skills@v1.4.0 --yes
gh skill-pack add dotnet/skills@main --plugin dotnet-msbuild --include "skills/build-perf/**"
gh skill-pack add my-org/my-skills@v2 --mode inline --yes
```

## Layout

```
.skills/
  manifest.yml              # intent — hand-editable
  manifest.lock.yml         # generated — do not edit
  .gitattributes            # `* text eol=lf` — keeps hashes stable across OSes
  plugins/
    <plugin>/
      skills/<skill>/SKILL.md
      skills/<skill>/scripts/...
      agents/<agent>.agent.md
AGENTS.md
  # ...your existing content...
  <!-- BEGIN gh-skill-pack managed -->
  ## Available skills (managed by gh-skill-pack — do not edit by hand)
  ...summary entries...
  <!-- END gh-skill-pack managed -->
.github/copilot-instructions.md   # optional — same managed block
```

## Manifest schema

### `.skills/manifest.yml` (intent)

```yaml
version: 1
plugins:
  - id: dotnet-msbuild
    source: dotnet/skills
    ref: v1.4.0
    include: ["skills/build-perf/**", "agents/msbuild/**"]   # optional
    install:
      mode: summary    # summary | inline | link
```

### `.skills/manifest.lock.yml` (generated)

```yaml
version: 1
managedBy: gh-skill-pack
toolVersion: 0.1.0
generatedAt: 2026-05-28T15:00:00Z
manifestHash: sha256:...
plugins:
  - id: dotnet-msbuild
    source:
      type: github
      host: github.com
      owner: dotnet
      repo: skills
    requestedRef: v1.4.0
    resolvedRef: a1b2c3d4...
    layout: dotnet-skills
    install:
      scope: repo
      mode: summary
    files:
      - path: .skills/plugins/dotnet-msbuild/skills/build-perf/SKILL.md
        upstreamPath: plugins/dotnet-msbuild/skills/build-perf/SKILL.md
        upstreamHash: sha256:...
        localHash: sha256:...
        mode: "0644"
```

## Cloud-agent integration modes

The Copilot **cloud agent** (and any contributor) reads `AGENTS.md` and
`.github/copilot-instructions.md`. `gh-skill-pack` writes a single managed block
in those files based on the install mode:

| Mode | What lands in AGENTS.md | When to use |
|---|---|---|
| `summary` (default) | Per-skill `### name` + 1-line description + repo-relative link to the SKILL.md | Most skills. Keeps AGENTS.md skim-able. |
| `inline` | Full SKILL.md body, fenced | Critical skills the agent must always read in full. |
| `link` | Just the bullet-list of links | Minimal noise; trust the agent to follow links. |

## Security model

- **No script execution.** `gh-skill-pack` never runs anything it installs.
- **Path-traversal rejection.** All entries are validated against the repo root before any IO. Tarball entries with absolute, drive-qualified, or `..` paths are refused, and we verify each resolved target lives under the destination via `filepath.Rel`.
- **Symlinks and hardlinks in tarballs are rejected** to avoid host-symlink path-traversal vectors.
- **Tarball caps.** Extraction is bounded to 5,000 files, 25 MB per file, and
  200 MB total to defend against compression bombs.
- **Pin to tags.** `gh-skill-pack` warns when you install from a branch ref
  (mutable). Prefer `@v1.4.0` or a commit SHA.
- **Lock-file paths are validated** on every read so a malicious checked-in
  lock cannot redirect writes outside `.skills/`.
- **Atomic writes** via `os.CreateTemp` in the target directory + rename, so
  a crashed install never leaves a half-written file in place of a good one.
- **Provenance in lock.** The resolved commit SHA and upstream hash for
  every file are recorded so `gh skill-pack verify` can detect tampering or
  drift.

## How it differs from `/plugin install`

| | `/plugin install` | `gh skill-pack add` |
|---|---|---|
| Scope | User's machine | The repository |
| Visible to cloud agent | ❌ No | ✅ Yes |
| Picked up by `@copilot` on github.com | ❌ No | ✅ Yes |
| Reproducible across contributors | ❌ No | ✅ Yes |
| Version-pinned + lockable | ❌ No | ✅ Yes |
| Verifiable | ❌ No | ✅ `gh skill-pack verify` |

## Development

```sh
git clone https://github.com/Evangelink/gh-skill-pack
cd gh-skill-pack
go build ./...
go test ./...
```

Install your local build into `gh`:

```sh
# produces ./gh-skill-pack (or gh-skill-pack.exe on Windows)
go build -o gh-skill-pack .          # use gh-skill-pack.exe on Windows
gh extension install .
gh skill-pack --help
```

## Roadmap (v1.1 +)

- Real 3-way merge on `gh skill-pack update` (fetch BASE by SHA, cache)
- `--scope=user` with host detection (Copilot CLI / Claude / Cursor / VS Code)
- `agentskills.io` standard layout support
- `gh skill-pack sync` to reconcile from hand-edited manifest
- URL specs (`https://github.com/.../blob/...`) and local specs (`./path`)
- Signature/provenance verification
- Private-repo auth, rate-limit handling
- Partial-failure recovery (transactional add/update)

## License

MIT — see [LICENSE](LICENSE).
