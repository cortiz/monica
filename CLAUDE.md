# Monica

Go CLI (`monica run`) that parses `.mon` files (raw HTTP request text) and sends them.
Module: `jmpeax.com/sec/monica` · Go 1.27 · cobra + zerolog.

## Layout
- `main.go` → `pkg/cmd` (cobra: `root.go`, `run.go`)
- `pkg/request` — `.mon` parser (`parser.go`), `Request` type; fixtures in `testdata/*.mon`
- `pkg/net` — HTTP client + response type
- `pkg/runner` — glue: parse file → send → print
- `pkg/tui` — `monica tui` (bubbletea); drives `pkg/runner` through its `Backend`
- `internal/logging` — zerolog setup

## Commands
- Build: `go build ./...` · Test: `go test ./...` · Single pkg: `go test ./pkg/request/ -run TestName`
- Lint (CI uses golangci-lint v2.15): `golangci-lint run ./...`
- Full PR gate: `make pr` (generate + test + fmt)
- `pkg/net` tests hit httpbin.org — they fail offline; run `go test ./pkg/request/...` for offline checks.

## Rules
- Run `gofmt` + the relevant package tests before saying a change is done.
- New parser behaviour needs a `testdata/*.mon` fixture and a table-driven test.
- Return errors, don't `log.Fatal`/`panic` in `pkg/`.
- No new dependencies without asking.
- Keep the `.mon` format in `Readme.md` in sync with parser changes.

## Claude workflow
Subagents live in `.claude/agents/`, skills in `.claude/skills/`.
For token-saving and parallel-agent practices, read `docs/claude-code.md` only when needed.
