---
name: parallel-review
description: Review the current branch with several reviewer subagents running in parallel (correctness, security, tests), then merge and dedupe their findings. Use when asked to review changes before a PR.
---

# Parallel review

1. Get the scope once: `git diff --stat main...HEAD` and `git status --short`. Don't read the full diff yourself — the reviewers will.
2. In a **single message**, launch these subagents together so they run concurrently:
   - `go-reviewer` — correctness, error handling, idioms
   - `security-reviewer` — injection, TLS, secrets, file handling
   - `test-writer` — prompt it: "List missing test cases for this diff; do NOT write files." (report-only)
   Give each the same short brief: the branch name, the `--stat` output, and "review `git diff main...HEAD`".
3. When all return, merge: drop duplicates, drop anything you can't confirm by reading the cited line, rank by severity.
4. Report a single list (max 10): `path:line — issue — why`. Offer to fix; don't fix unasked.
