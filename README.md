# claude-manager

A Claude Code plugin + Go CLI (`cmgr`) for coordinating one feature across multiple repos from a single "manager" Claude session. Drives N headless `claude -p` worker sessions, one per repo. Subscription billing only — `ANTHROPIC_API_KEY` is stripped from worker env so spend always hits your Pro/Max OAuth.

## Why

Coordinating a feature across multiple repos in a single Claude session means loading every repo into one bloated context. With `claude-manager`:

- One interactive manager session holds the plan + shared contract.
- One headless worker session per repo (UUID-tracked, resumable, locked).
- Manager dispatches work via `cmgr send` / `broadcast`, polls via `cmgr inbox` / `status`.
- Contract is a plain markdown file the manager edits directly.

## Install

```sh
git clone https://github.com/wolzey/claude-manager.git
cd claude-manager
make install-plugin
```

This:
1. Builds `plugins/claude-manager/bin/cmgr` (requires Go 1.21+).
2. Registers this repo as a local Claude Code marketplace named `wolzey` (`claude plugin marketplace add .`).
3. Installs the plugin: `claude plugin install claude-manager@wolzey`.
4. Symlinks `cmgr` to `~/.local/bin/cmgr` so the binary is on your shell PATH.
5. Adds `Bash(cmgr *)` to the permissions allowlist so the manager session doesn't get prompted on every call.

**Restart your interactive `claude` session** for the plugin to load.

After restart, `/claude-manager:manager <project>` is available, and `cmgr` is on PATH for both the manager and your normal shell. The plugin shows up in `/plugin` and in `claude plugin list`.

## Quick start

In a fresh `claude` session:

```
/claude-manager:manager my-feature
```

…and follow the prompts to register the repos. Or do it from the shell:

```sh
cmgr project new my-feature --description "Add X-Request-ID header end-to-end"
cmgr worker add my-feature backend  --repo /abs/path/to/api-svc
cmgr worker add my-feature frontend --repo /abs/path/to/cart-widget
cmgr send my-feature backend "Describe the current /orders response shape."
cmgr send my-feature frontend "Where does the widget read response headers?"
cmgr contract edit my-feature             # write the agreed shape
cmgr broadcast my-feature "contract.md updated — implement your side."
cmgr inbox my-feature --consume           # collect async results
```

## CLI surface

See `cmgr <command> --help` for full options.

```
cmgr project   new | ls | show | rm
cmgr worker    add | ls | show | rm
cmgr send      <project> <worker> "<msg>" [--detach] [--budget N] [--model M]
cmgr broadcast <project> "<msg>"          [--detach] [--budget N]
cmgr log       <project> <worker>          [--raw] [--follow]
cmgr status    <project>
cmgr inbox     <project>                   [--consume] [--format json]
cmgr contract  show | edit | path  <project>
```

## State layout

The plugin is the binary + commands + skill. The CLI's project state lives outside the plugin (so reinstalling/upgrading the plugin doesn't lose your work):

```
~/.claude/manager/                       # CLI state (not in plugin dir)
  config.yaml                            # viper defaults (budget, model, allowed tools)
  projects/<slug>/
    project.json
    contract.md                          # the shared blackboard
    workers/<name>.json                  # session_id, repo, allowed tools, initialized flag
    logs/<name>.jsonl                    # raw stream-json transcript per send
    logs/<name>.last.txt                 # cached final answer
    inbox.jsonl                          # async completion events
    locks/<name>.lock                    # flock-protected, one in-flight send per worker

<repo-root>/                              # the marketplace
  .claude-plugin/marketplace.json        # marketplace registry (one plugin: claude-manager)
  plugins/claude-manager/                # the plugin
    .claude-plugin/plugin.json           # plugin manifest
    plugin.json                          # mirror (cross-tool compat)
    commands/manager.md                  # → /claude-manager:manager
    skills/claude-manager/SKILL.md
    bin/cmgr                             # built by `make build`
  cmd/cmgr/main.go                       # Go source at repo root
  internal/{cmd,store,runner,config}/
  go.mod
  Makefile                               # orchestrates build + install
  scripts/allow-cmgr.py                  # adds Bash(cmgr *) permission
```

## Implementation notes

- `claude -p --session-id <uuid>` is **create-only** in 2.1.x — subsequent calls must use `--resume <uuid>`. Worker JSON tracks `initialized` to pick the right flag.
- `--output-format stream-json` requires `--verbose`. Runner passes both.
- Runner strips `ANTHROPIC_API_KEY` from child env unconditionally. Verify via `apiKeySource: none` (or `subscription`) in the `system:init` line of `logs/<name>.jsonl`.
- Concurrency: one in-flight send per worker (POSIX `flock`). Parallel = across workers only.
- Default permission mode: `acceptEdits` (auto-approve edits, prompt on Bash). Override per worker with `--allowed-tools`.
- Default budget: $5 per send. Override with `--budget` or `~/.claude/manager/config.yaml`.

## Uninstall

```sh
make uninstall-plugin
```

Reverses `install-plugin`: `claude plugin uninstall claude-manager@wolzey`, `claude plugin marketplace remove wolzey`, drops the `Bash(cmgr *)` permission, removes the `~/.local/bin/cmgr` symlink. Does **not** delete `~/.claude/manager/` state or this repo.

## License

MIT.
