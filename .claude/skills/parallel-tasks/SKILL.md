---
name: parallel-tasks
description: Split a multi-part change into independent pieces and run one subagent per piece in its own git worktree, in parallel, then integrate. Use when the user asks for several independent changes at once or says "do these in parallel".
---

# Parallel tasks with worktrees

Only parallelize work that touches **different files/packages** (e.g. `pkg/request` vs `pkg/net`). If two pieces edit the same file, do them sequentially.

1. Write a 1-line plan per piece: goal, files it owns, how to verify (`go test ./pkg/<x>/...`).
2. In a **single message**, launch one `general-purpose` agent per piece with `isolation: "worktree"`. Each brief must be self-contained:
   - the goal and acceptance check
   - the files it may touch (and that it must not touch others)
   - "Run gofmt and `go test ./pkg/<x>/...`; commit on your worktree branch; reply with the branch name, a 3-line summary, and test output tail."
3. When they return, merge each branch into the working branch one at a time (`git merge --no-ff <branch>`), running `go build ./... && go test ./pkg/request/...` after each.
4. On conflict: resolve it yourself, don't re-dispatch.
5. Run `go-reviewer` once on the combined diff.

Keep it to ≤4 parallel agents — beyond that, merge cost and token spend outweigh the speed-up.
