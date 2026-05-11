---
name: claude-manager
description: Drive headless `claude -p` worker sessions across multiple repos via the `cmgr` CLI. Use this when the user wants to "coordinate across repos", "manage worker sessions", "dispatch to another repo", "run a manager session", or whenever you see the `cmgr` command being invoked. Provides project + worker CRUD, sync/async message dispatch, a shared contract.md blackboard, lock-based concurrency control, and an inbox for async completions. Subscription billing only — the CLI strips ANTHROPIC_API_KEY from worker env.
---

# claude-manager skill

`cmgr` is a Go CLI shipped with this plugin (at `<plugin-dir>/bin/cmgr`, symlinked to `~/.local/bin/cmgr`). It wraps `claude -p` to drive multiple headless sessions across repos. The interactive session you're in right now is the "manager"; the headless sessions `cmgr` spawns are "workers."

The slash command `/claude-manager:manager <project>` boots a manager session for a project.

State lives at `~/.claude/manager/projects/<slug>/`:
- `project.json` — metadata
- `contract.md` — the shared blackboard (manager edits this directly)
- `workers/<name>.json` — `{name, session_id, repo_path, allowed_tools, initialized, status, last_run_at}`
- `logs/<name>.jsonl` — raw stream-json transcript of each send
- `logs/<name>.last.txt` — cached final answer text
- `inbox.jsonl` — async completion events awaiting consumption
- `locks/<name>.lock` — advisory flock; held while a worker is running

## Command surface

### Projects
```
cmgr project new <name> [--description "..."] [--no-edit]
cmgr project ls
cmgr project show <name>
cmgr project rm <name> --yes
```

### Workers
```
cmgr worker add <project> <name> --repo <abs-path> [--allowed-tools "..."] [--model opus]
cmgr worker ls <project>
cmgr worker show <project> <name>
cmgr worker rm <project> <name>
```

`--repo` must be absolute. The worker name is the human-readable handle; session UUIDs are auto-allocated.

### Messaging
```
cmgr send <project> <worker> "<message>" [--detach] [--budget 5] [--model opus]
cmgr broadcast <project> "<message>" [--detach] [--budget 5]
cmgr log <project> <worker> [--raw] [--follow]
cmgr status <project>
cmgr inbox <project> [--consume] [--format json]
```

- **Sync send** (default): blocks until the worker replies, prints final assistant text to stdout, exits nonzero on worker error.
- **`--detach`**: fork-and-forget; completion lands in `inbox.jsonl`. Use for parallel fan-out.
- **`broadcast`**: same message to all workers; parallel if `--detach`, sequential and goroutine-fanned-out if not.
- **`cmgr log`** default prints the cached last-result text. `--raw` dumps the full stream-json transcript (verbose; useful for debugging).
- **`cmgr inbox`** without `--consume` is non-destructive; with `--consume` it truncates after printing.

### Contract
```
cmgr contract show <project>
cmgr contract edit <project>
cmgr contract path <project>
```

`contract.md` is plain markdown. Manager sessions can also Read/Edit it directly via Claude's tools — `cmgr contract path` just resolves the absolute path.

## Key patterns

### Cross-repo contract change
1. Manager and user decide a shape change ("the API now returns `subscription_id` as snake_case").
2. Manager **edits `contract.md`** under the relevant section (e.g. `## API / Event Shapes`).
3. Manager runs `cmgr broadcast <proj> "contract.md updated — see <path>; acknowledge the new shape"`.
4. Workers reply with their plan / objections.
5. Manager relays summary to the user.

### Q&A across repos
- Sync: `cmgr send proj backend "How does the orders endpoint serialize line items today?"` — manager waits, gets answer inline.
- Parallel: `cmgr broadcast proj "What's the build command in your repo?" --detach` then `cmgr inbox proj --consume`.

### Implement on one side, verify on the other
- `cmgr send proj backend "Add X-Request-ID header to the /orders endpoint. Use the shape in contract.md."` — sync, get diff confirmation.
- `cmgr send proj frontend "Read the request to /orders and confirm X-Request-ID is now propagated."` — sync, verify.

## Failure modes & recovery

- **`worker "X" is busy (lock held)`** — another send is in flight. Run `cmgr status <proj>`. If a `running` worker is actually dead (e.g. CLI crashed), `rm ~/.claude/manager/projects/<slug>/locks/<worker>.lock` to clear. Last resort.
- **`Session ID ... is already in use`** — should not happen post-v1; means Worker.initialized desynced from the actual claude session state. Fix: `python3 -c "import json,pathlib; p=pathlib.Path('~/.claude/manager/projects/<slug>/workers/<name>.json').expanduser(); d=json.loads(p.read_text()); d['initialized']=True; p.write_text(json.dumps(d, indent=2))"`.
- **`claude exited (exit status 1) without producing a result line`** — worker process failed before emitting a result. Check `cmgr log <proj> <worker> --raw` for the error. Common causes: budget exhausted, invalid `--allowed-tools` syntax, repo path no longer exists.
- **Budget exhausted** — `total_cost_usd` reached `--max-budget-usd`. Re-send with `--budget 10` (or whatever).
- **Worker burns through quota** — manager sees `apiKeySource: subscription` (or `none` for cached OAuth) in stream-json init. If billing-source ever shows `api_key`, the env was wrong — the runner already strips `ANTHROPIC_API_KEY`, so a stray non-OAuth path means deeper config issue.

## Constraints

- One in-flight send per worker (flock). Parallel sends are across-worker only.
- Workers default to `--permission-mode acceptEdits` (auto-approve file edits, still prompt on Bash). Override via `cmgr worker add --allowed-tools` to constrain tools per worker.
- Default budget per send: $5 (configurable in `~/.claude/manager/config.yaml` as `default_budget_usd`).
- Subscription billing only. `ANTHROPIC_API_KEY` is stripped from the worker's env unconditionally.

## When to invoke

This skill is most relevant when:
- The user runs `/manager` (the slash command).
- The user mentions coordinating a feature across repos, mediating between services, or running parallel implementations.
- You see `cmgr` in shell history or recent commands.
- The user asks about worker status, inbox, or contracts in this system.
