# Working on Monica with Claude Code

This repo ships a shared Claude Code setup. Everything here is optional for humans; it's what Claude reads.

## What's configured

| Path | Purpose |
|---|---|
| `CLAUDE.md` | Project memory, loaded into **every** session. Kept short on purpose. |
| `.claude/settings.json` | Shared permissions (allow go/make/git read cmds, deny secrets & build output) + a gofmt hook. |
| `.claude/hooks/gofmt.sh` | Runs `gofmt -w` on any `.go` file Claude edits. |
| `.claude/agents/explorer.md` | Haiku, read-only. Cheap code search that returns `file:line`, not file dumps. |
| `.claude/agents/go-reviewer.md` | Sonnet, read-only. Correctness / error-handling review. |
| `.claude/agents/security-reviewer.md` | Sonnet, read-only. Injection, TLS, secret leakage, file handling. |
| `.claude/agents/test-writer.md` | Sonnet. Writes table-driven tests; only touches `*_test.go` and `testdata/`. |
| `.claude/skills/parallel-review` | `/parallel-review` — runs the reviewers concurrently and merges findings. |
| `.claude/skills/parallel-tasks` | `/parallel-tasks` — one agent per independent piece, each in its own git worktree. |

Personal overrides go in `.claude/settings.local.json` (gitignored).

## Best practices

- **Explore → plan → code → verify.** For anything non-trivial, use plan mode (Shift+Tab) first, approve the plan, then implement.
- **Give a check.** "Fix X; `go test ./pkg/request/...` must pass" beats "fix X". Claude iterates against a concrete target.
- **Be specific about scope.** Name the file/function (`pkg/request/parser.go`, `ParseMonFile`) instead of "the parser".
- **Small diffs, frequent commits.** Easier to review, easier to revert, cheaper context.
- **Add a fixture for every parser change** (`pkg/request/testdata/*.mon`).
- **Review before PR**: `/parallel-review`, or `/code-review` / `/security-review` built-ins.
- **Update `CLAUDE.md` when Claude repeats a mistake** — one line, imperative. Delete stale lines.

## Token savings

Context is the main cost driver: everything in the window is re-sent every turn.

1. **Keep `CLAUDE.md` lean.** It's loaded every turn. Put long guides (like this file) elsewhere and point to them.
2. **`/clear` between unrelated tasks.** Don't carry a debugging session into a new feature.
3. **`/compact <focus>`** when a long task gets heavy, e.g. `/compact keep the parser design decisions`.
4. **Check usage** with `/context` (what's in the window) and `/cost`.
5. **Delegate searches to `explorer`** (Haiku). It reads the files; you get back 15 lines, not 500.
6. **Pick the model per job.** Haiku for search/simple edits, Sonnet for most coding, Opus for hard design/debugging. Switch with `/model`. Subagents already pin cheaper models.
7. **Point, don't paste.** Reference `@pkg/net/http.go` or a line range instead of pasting code or logs. For logs, paste only the failing part.
8. **Deny noise.** `settings.json` blocks reading `dist/`, the built binary, and coverage output.
9. **Hooks over instructions.** The gofmt hook means Claude never spends a turn formatting.
10. **Prefer targeted tests.** `go test ./pkg/request/ -run TestParse` instead of `-v ./...` output.
11. **Headless for repeatable chores:** `claude -p "run golangci-lint and fix issues in pkg/net" --allowedTools "Edit,Bash(golangci-lint run:*)"`.

## Multiple agents in parallel

**Inside one session (subagents).** Claude can launch several subagents in one message; they run concurrently, each with its own context window, and only their summaries return to the main thread — which also saves tokens.
- `/parallel-review` — go-reviewer + security-reviewer + test gap analysis at once.
- `/parallel-tasks` — independent changes in separate worktrees, merged afterwards.
- Ad hoc: "Use 3 explorer agents in parallel to find how headers, body, and comments are parsed."

**Across sessions (git worktrees).** Run separate Claude sessions on separate branches without them stepping on each other:

```bash
git worktree add ../monica-parser -b feat/parser-crlf
git worktree add ../monica-net    -b feat/http-timeouts
(cd ../monica-parser && claude)   # terminal 1
(cd ../monica-net    && claude)   # terminal 2
# when done
git worktree remove ../monica-parser
```

Rules of thumb:
- Parallelize only work on **disjoint files/packages**. Same-file work → sequential.
- 2–4 agents is the sweet spot; beyond that, merging and token cost outweigh the speed-up.
- Each agent's brief must be self-contained: goal, files it owns, how to verify.
- One agent writes, another reviews — a fresh context catches what the author missed.
