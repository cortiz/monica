# Monica Roadmap

Goal: one Go binary that **runs HTTP requests** (like curl/Bruno), **tests them with
JavaScript** (embedded goja, no Node.js), and **load-tests** the same files (like k6).

Each task is sized to be one PR: code + `testdata/*.mon` fixture + table-driven tests +
`Readme.md` update when the `.mon` format or CLI changes (see `CLAUDE.md`).

Legend: **S** < half a day · **M** ~1 day · **L** 2–3 days · 🔒 needs dependency approval

---

## Phase 0 — Foundations (fix what exists) ✅ done

The current code has bugs that every later phase builds on. Fix them first.

| ID | Task | Size |
|----|------|------|
| 0.1 | Error handling end-to-end | M |
| 0.2 | Parser correctness fixes | S |
| 0.3 | HTTP client hardening | S |
| 0.4 | Tooling cleanup | S |

**0.1 Error handling end-to-end**
- `ParseRequest`/`ParseMonFile` return `(*Request, error)`; no logging-and-return-nil.
  Today a missing file returns `nil` → `net.HTTPRequest(nil, …)` panics.
- `runner.RunSingleFile` returns an error; `run` command uses `RunE`.
- Exit code: `0` all ok, `1` any request/test failed, `2` usage/config error.
- Parse errors include `file:line`.
- ✅ Done when: missing file / bad request line produce a clear message and non-zero exit; tests cover each.

**0.2 Parser correctness fixes**
- Header split on first `:` only (`strings.Cut`), trim spaces; `X-Foo: a: b` currently dropped.
- Headers become `http.Header`-style (`map[string][]string`) so repeated headers survive.
- Body has a spurious leading `\n` (join bug) — fix and preserve trailing content exactly.
- Request line: support optional `HTTP/1.1` suffix; validate method + URL.
- Fix `testdata/simple_post.mon` + Readme example (says POST, uses GET).
- ✅ Done when: table-driven tests cover each case with fixtures.

**0.3 HTTP client hardening**
- One shared `*http.Client` with a timeout (configurable later in 2.x).
- Don't always `Add` a second `User-Agent`; only set default when absent.
- Response: keep `Duration`, sorted headers on print, `http.Header` type.
- Move httpbin tests behind `httptest.Server` so `go test ./...` works offline.

**0.4 Tooling cleanup**
- Makefile: `$(BiNARY_NAME)` typo, `golint` → `golangci-lint run ./...`, goreleaser `--rm-dist` → `--clean`.
- CI runs `go test ./...` offline-safe after 0.3.

---

## Phase 1 — `.mon` format v2 ✅ done

| ID | Task | Size |
|----|------|------|
| 1.1 | Multiple requests per file | M |
| 1.2 | Named requests & metadata | S |
| 1.3 | Script blocks (parse only) | M |
| 1.4 | Body from file | S |

**1.1 Multiple requests per file** — separator line `###` (optional title after it).
`ParseMonFile` returns `[]*Request`; runner executes in order.

**1.2 Named requests & metadata** — `# @name login`, `# @timeout 5s`, `# @skip`.
Name is used in reports and for chaining (Phase 6).

**1.3 Script blocks (parse only, no execution yet)**
```
< {%  /* pre-request JS */  %}
GET {{baseUrl}}/users
...body...

> {%  /* post-response JS (tests) */  %}
```
Also `> ./tests/users.js` to reference a file. Store as `Request.PreScript` / `PostScript`
with source location for error messages.

**1.4 Body from file** — `< ./payload.json` as body line (path relative to `.mon` file).

✅ Phase done when: Readme documents the full v2 grammar; fixtures for every construct.

---

## Phase 2 — Configuration & variables (`monica.yml`) ✅ done

Uses `gopkg.in/yaml.v3` (already an indirect dep → promote to direct).

| ID | Task | Size |
|----|------|------|
| 2.1 | `internal/config`: load & validate `monica.yml` | M |
| 2.2 | Environments + `${OS_ENV}` + `.env` + local override | M |
| 2.3 | `{{var}}` template substitution | M |
| 2.4 | CLI flags `--env`, `--var`, `--config` | S |
| 2.5 | Secret masking | S |

**2.1 Config loader**
```yaml
version: 1
defaults: { timeout: 30s, followRedirects: true, insecureSkipVerify: false,
            headers: {User-Agent: monica/0.2.0}, vars: {apiVersion: v2} }
envs:
  dev:  { vars: { baseUrl: http://localhost:8080, pass: ${DEV_PASSWORD} } }
  prod: { timeout: 10s, vars: { baseUrl: https://api.example.com } }
scripts: { paths: [./lib] }
load:    { vus: 50, duration: 30s, thresholds: ["p95<300ms", "errors<1%"] }
```
- Discovery: walk up from the `.mon` file's dir to find `monica.yml`; `--config` overrides.
- Strict decoding (unknown keys = error), validate durations, `version`.

**2.2 Environments** — selected env merges over `defaults`. `${NAME}` resolved from OS env,
then `.env` file (stdlib parser, no dep). `monica.local.yml` deep-merges on top (gitignored).
`MONICA_ENV` = default for `--env`.

**2.3 Templating** — `{{name}}` in URL, headers, body. Resolution order (high → low):
`--var` → runtime `vars.set()` (Phase 3) → env vars → `defaults.vars`.
Unknown variable = error with `file:line`. Built-ins: `{{$uuid}}`, `{{$timestamp}}`, `{{$randomInt}}`.

**2.4 CLI** — `monica run --env dev --var token=abc --config ./monica.yml`.

**2.5 Secret masking** — values originating from `${…}` are replaced by `****` in logs and printed output.

---

## Phase 3 — Script engine (goja) `github.com/dop251/goja` ✅ done

| ID | Task | Size |
|----|------|------|
| 3.1 | `pkg/script`: engine wrapper & sandbox | L |
| 3.2 | `req` / `res` / `vars` API | M |
| 3.3 | Pre-request scripts | S |
| 3.4 | Script errors & timeouts | S |

**3.1 Engine** — `Engine` owns one `*goja.Runtime` (not goroutine-safe → one per VU later).
Compile each script once (`goja.Compile`) and cache the `*goja.Program`.
Only exposed globals: `console`, `req`, `res`, `vars`, `test`, `expect`. No fs/net/process.

**3.2 API**
- `res.status`, `res.headers.get(name)`, `res.body` (string), `res.json()`, `res.time` (ms).
- `req.method/url/headers/body` (mutable in pre-request scripts).
- `vars.get/set/has`, `monica.env` (current env name).

**3.3 Pre-request** — `< {% %}` runs before templating, can set vars and mutate `req`.

**3.4 Errors** — JS exceptions map to `file:line` in the `.mon` file; per-script timeout
via `runtime.Interrupt`.

---

## Phase 4 — Tests & reporting ✅ done

| ID | Task | Size |
|----|------|------|
| 4.1 | `test()` + `expect()` assertion API | M |
| 4.2 | Console reporter | M |
| 4.3 | Machine reporters: JSON, JUnit XML | M |
| 4.4 | Run controls: `--bail`, `--filter`, `--quiet` | S |

**4.1 Assertions** (implemented in Go, exposed to JS — no chai dependency)
```js
test("returns user", () => {
  expect(res.status).toBe(200);
  expect(res.json().name).toEqual("ada");
  expect(res.time).toBeLessThan(500);
});
```
Matchers: `toBe, toEqual (deep), toContain, toMatch, toBeLessThan/GreaterThan, toHaveProperty, toBeTruthy`, `.not`.

**4.2 Console reporter** — per request: name, status, duration, ✓/✗ per test, failure diff; summary line; exit code 1 on failure.

**4.3 Reporters** — `--reporter json|junit --out report.xml` for CI.

**4.4 Run controls** — stop at first failure, run by request name/glob, hide bodies.

---

## Phase 5 — Modules & code reuse `github.com/dop251/goja_nodejs` ✅ done

| ID | Task | Size |
|----|------|------|
| 5.1 | `require()` for local files (sandboxed) | M |
| 5.2 | `node_modules` CommonJS packages | S |
| 5.3 | Built-in `monica/*` modules (Go) | M |
| 5.4 | Folder-level hooks `monica.js` | M |
| 5.5 | ESM/TypeScript via esbuild | L |

**5.1** Custom `SourceLoader` restricted to project root + `scripts.paths`; path traversal rejected.
Compiled modules cached and shared across runtimes.

**5.2** Node-style resolution (`node_modules`, `package.json#main`). Document: pure-JS CJS only
(lodash, dayjs, ajv, uuid, crypto-js work; axios/fs-based packages don't).

**5.3** `monica/crypto` (hmac, sha256, md5), `monica/encoding` (base64, url, hex),
`monica/random` (uuid, int, string), `monica/jsonpath`.

**5.4** `monica.js` in a folder exports `beforeAll/beforeEach/afterEach/afterAll`.
Applies to the folder and below, up to the project root; `beforeAll` runs lazily before the
folder's first executed request, `afterAll` at the end of the run (see Readme).

**5.5** Only if needed: embed `github.com/evanw/esbuild/pkg/api` to transpile ESM/TS on load.
Done: `.ts/.mts/.cts/.mjs` and `.js` files with `import`/`export` are transpiled to CommonJS
on load (modules, script files and `{% %}` blocks), with inline source maps for error lines.

---

## Phase 6 — Collections & chaining ✅ done

| ID | Task | Size |
|----|------|------|
| 6.1 | Deterministic run order | S |
| 6.2 | Shared variable scope across a run | S |
| 6.3 | Data-driven runs (CSV/JSON) | M |
| 6.4 | Retries & conditional skip | S |

**6.1** Files sorted by path; requests in file order; optional `order:` list in `monica.yml`.
Done: path order is folder by folder; `order:` entries are files or folders, per config.
**6.2** `vars.set` in one request is visible to the following ones (login → token → use).
Done (already true since phase 3; now tested across files and `monica.yml` projects).
**6.3** `monica run --data users.csv` iterates rows as vars.
Done: `.csv` (header row) or `.json` (array of objects); each row runs every selected file;
precedence `--var` > `vars.set` > row > config; `monica.iteration`; reports name the row.
**6.4** `# @retry 3 1s`; `req.skip()` from pre-request script.
Done: `beforeEach` hooks can also call `req.skip()`; retries cover connection errors,
script/hook errors and failed tests (not input errors); only the last attempt is reported.

---

## Phase 7 — Load testing (`monica load`) ✅ done

| ID | Task | Size |
|----|------|------|
| 7.1 | VU executor (fixed VUs × duration/iterations) | L |
| 7.2 | Metrics collection & percentiles | M |
| 7.3 | Thresholds & exit code | S |
| 7.4 | Arrival-rate executor + ramping stages | M |
| 7.5 | Live progress + summary/JSON output | M |
| 7.6 | Connection & resource tuning | S |

**7.1 Executor** — each VU = goroutine + own goja runtime + own vars; loops over the
collection. Graceful stop on duration / Ctrl-C (context cancel).
✅ Done: `monica load --vus/--duration/--iterations/--graceful-stop`, defaults from
`load:`; one `runner.Runner` per VU (vars, hooks, `monica.vu`); shared iterations; input
errors abort with exit 2; Ctrl-C prints the summary (exit 1). Plain counts only until 7.2.

**7.2 Metrics** — per request name: count, errors, min/avg/p50/p90/p95/p99/max, RPS,
bytes; `httptrace` timings (DNS, connect, TLS, TTFB). Stdlib-only histogram
(HDR-style buckets) — no dependency.
✅ Done: per request (file:line) and total; log-linear histogram (64 buckets per power of
two, <1.6% error, mergeable — one per VU, merged at the end); phases DNS / connect / TLS /
waiting / receiving; body bytes sent/received. Summary table on stdout (JSON in 7.5).

**7.3 Thresholds** — from `load.thresholds` or `--threshold "p95<300ms"`; failing → exit 1.
✅ Done: `[request:]metric op value` with min/avg/med/max/pNN/p(NN.N)/errors/rps; flags
replace the config list; invalid ones are usage errors (exit 2) before the test starts.

**7.4 Executors** — `--rate 100/s` (constant arrival) and `stages:` ramp-up/down in `monica.yml`.
✅ Done: four modes — fixed VUs, ramping VUs (`--stage`/`load.stages`), constant arrival
rate (`--rate`/`load.rate`, VU pool up to `--vus`, dropped iterations counted) and ramping
rate (stages + rate). Arrival times are computed exactly from the integral of the ramped rate.

**7.5 Output** — live line (VUs, RPS, p95, errors) on TTY; final table; `--out json`.
✅ Done: progress line on stderr every second when it is a terminal (`-q` off), rps/p95
over the last second from a small shared interval histogram; `-o/--out file.json` (or `-`
for stdout) with config, totals, per-request latency/phases and thresholds.

**7.6 Tuning** — shared `Transport` with `MaxIdleConnsPerHost`, keep-alive toggle,
`--insecure`, per-VU cookie jar.
✅ Done: one `net.Transports` pool per test (idle conns per host = max VUs; shared by all
VUs), `--no-keepalive`, `-k/--insecure` (also on `monica run`), a cookie jar per VU.

---

## Phase 8 — Polish & distribution ✅ done

- 8.1 `monica init` — scaffold `monica.yml`, example `.mon`, `.gitignore` entries.
  ✅ Done: `monica init [dir]` (`pkg/scaffold`, embedded templates) writes `monica.yml`
  (`baseUrl` from `--base-url`, `dev` env), `example.mon` (GET + POST with tests) and appends
  `.env` / `monica.local.yml` to `.gitignore`; existing files kept unless `--force`.
- 8.2 `monica import curl '<cmd>'` → `.mon` (Postman/Bruno import live in Phase 9).
  ✅ Done: `pkg/importer` (`Request` + `WriteMon`, refuses bodies the format can't hold — the
  shared writer for 9.2/9.3) and `pkg/importer/curl` (shell-word splitter incl. `$'…'` and
  `^` continuations; method/headers/data/`--json`/`-G`/`-u`/`-T`/`-m`; warnings for the
  rest). Command from an argument, stdin or after `--`; `-o`, `--force`, `--name`.
  `-F` / `--form-string` landed with multipart in 8.3.
- 8.3 Cookies (`cookie jar` per run for `monica run`; `monica load` has one per VU since 7.6), multipart/form-data, file upload.
  ✅ Done: `monica run` keeps one cookie jar per run (`--no-cookies` turns it off).
  Multipart bodies are written as raw HTTP parts under a literal `multipart/*; boundary=`
  Content-Type; lines are sent CRLF and a part that is a single `< path` line uploads that
  file as is (`Request.Parts`: text parts expanded, file parts not). Whole-body uploads were
  already `< path`. `import curl` converts `-F`.
- 8.4 Docs site / full format reference; examples folder.
  ✅ Done: `examples/` (one feature per folder, against httpbin.org, with a README) run by
  `pkg/cmd/examples_test.go` against a local httpbin stand-in; `Readme.md` stays the full
  reference (contents line, features list refreshed). A hosted docs site was not set up (no
  generator dependency); the Markdown renders on GitHub.
- 8.5 Release: goreleaser + Docker image updated with new commands.
  ✅ Done: `internal/version` (set by `-ldflags -X …Version=`, else the module version,
  else `dev`) drives `monica --version` and the default `User-Agent`. goreleaser: ldflags,
  `-trimpath`, `go generate` hook, `formats`, checksums, archives ship the Readme and
  `examples/`, unsupported windows/darwin targets ignored. Dockerfile: static build with
  the version as a build arg, pinned Alpine with CA certificates, non-root user,
  `ENTRYPOINT ["monica"]`, `WORKDIR /work`; `.dockerignore`. The Docker workflow now runs on
  `v*` tags (it was watching branches) with semver image tags; `make snapshot` / `make docker`.
  Homebrew: each release updates a cask in `cortiz/homebrew-tap` (GoReleaser
  `homebrew_casks`; needs the `HOMEBREW_TAP_GITHUB_TOKEN` secret, skipped without it and for
  pre-releases): `brew install cortiz/tap/monica`.

---

## Phase 9 — Integrations & UI ✅ done

Independent tracks: they can be picked up in any order once their prerequisites are done.

| ID | Task | Size | Needs |
|----|------|------|-------|
| 9.1 | TUI (`monica tui`) ✅ | L | Phases 1, 2 (better with 4) |
| 9.2 | Import from Postman ✅ | L | Phases 1, 2 (scripts: 3–4) |
| 9.3 | Import from Bruno ✅ | L | Phases 1, 2 (scripts: 3–4) |

**9.1 TUI** — interactive terminal UI over the same runner (no separate logic).
🔒 `github.com/charmbracelet/bubbletea` + `bubbles` + `lipgloss`.
- 9.1.1 Layout: collection tree (folders → `.mon` files → requests) | request view | response view.
- 9.1.2 Environment picker (from `monica.yml`), resolved variables panel (secrets masked).
- 9.1.3 Send request; response tabs: body (pretty JSON, syntax highlight), headers, timings, tests ✓/✗.
- 9.1.4 Open request in `$EDITOR`, reload on save; history of the session's runs.
- 9.1.5 Load-test dashboard: live VUs, RPS, p95, error rate (reuses Phase 7 metrics stream).
- Keep the runner emitting events (started / response / test result) so CLI reporters and
  the TUI consume the same stream — design this into 4.2.
- ✅ Done when: everything doable in the TUI is also doable via `monica run`; TUI has model-level tests (bubbletea `Update` is pure).
- ✅ Done: `monica tui` (`pkg/tui`) with bubbletea v2.1.1, bubbles v2.2.1, lipgloss v2.0.6 (`charm.land/…`; v1
  queried the terminal at startup of every command). Tree / request / response layout with body
  (pretty, colored JSON), headers, timings, tests and log tabs; env picker and variables
  panel (masked, with sources); `$EDITOR` at the request line, reload on exit and on file
  change; session history; live load dashboard (VUs, rps sparkline, p95, failures, summary
  and thresholds). Sends go through `runner.RunRequestAt` on one Runner per environment, so
  chaining, cookies and hooks behave like one `monica run`; results carry
  `ResponseStatus/Headers/Body` for the tabs; logs go to a buffer while the UI runs.
  Model tests drive `Update` with a fake `Backend`; a session test covers the real runner.

**9.2 Import from Postman** — `monica import postman <collection.json> [--env env.json] --out ./dir`
- 9.2.1 Parse Collection **v2.1** JSON (stdlib `encoding/json`); folders → directories, items → `.mon` files.
- 9.2.2 Requests: method, URL (raw + query), headers, bodies (raw, urlencoded, form-data, file refs).
- 9.2.3 Variables: collection vars + environment files → `monica.yml` (`defaults.vars`, `envs.<name>`);
  `{{var}}` syntax already matches. Secret-typed values → `${ENV_NAME}` placeholders, never written in clear.
- 9.2.4 Auth blocks (bearer, basic, API key) → headers with variables.
- 9.2.5 Scripts: copy pre-request/test scripts into script blocks; ship a `pm` compatibility shim
  (`pm.test`, `pm.expect` subset, `pm.response.json()/code/headers`, `pm.environment/collectionVariables.get/set`).
  Unsupported calls → `// TODO(monica-import):` comments + a summary warning.
- ✅ Done when: fixture collections (simple, nested folders, auth, scripts) import and the result runs; import report lists everything not converted.
- ✅ Done: `monica import postman` (`pkg/importer/postman`) on the shared `importer.Project` /
  `WriteProject` (files, hooks, `monica.yml`, `IMPORT_REPORT.md`, `.env.example`). Folders →
  directories, one `.mon` per request in `order:`; variables and environments with secrets
  as `${NAME}`; bearer / basic / API key / OAuth 2.0 token auth with inheritance; raw,
  urlencoded, form-data, file and GraphQL bodies; scripts behind a `pm` shim
  (`lib/monica-postman.js`); collection and folder scripts as `monica.js` hooks. The fixture
  collection imports and runs end to end against a stand-in API (`pkg/cmd`).

**9.3 Import from Bruno** — `monica import bruno <collection-dir> --out ./dir`
- 9.3.1 Write a `.bru` parser (Bru lang has no Go library): `meta`, `get/post/…`, `headers`,
  `params:query`, `body:json|text|form-urlencoded|multipart-form`, `auth:*`, `vars:pre-request/post-response`,
  `script:pre-request/post-response`, `tests`, `assert`.
- 9.3.2 `bruno.json` + `collection.bru` + `folder.bru` → `monica.yml` defaults / folder `monica.js` hooks (5.4).
- 9.3.3 `environments/*.bru` → `envs.<name>`; `vars:secret` → `${ENV_NAME}` placeholders.
- 9.3.4 Scripts/tests: copy into script blocks with a `bru` / Bruno-`res` compatibility shim
  (`bru.setVar/getVar/getEnvVar`, `res.getStatus()/getBody()/getHeader()`); `assert` blocks → generated `expect()` calls.
- 9.3.5 Keep Bruno's `seq` ordering (maps to 6.1 order).
- ✅ Done when: fixture collections round-trip into runnable `.mon` files with an import report of unconverted items.
- ✅ Done: `monica import bruno` (`pkg/importer/bruno`): a `.bru` parser (dictionary, text and
  list blocks, `~` disabled entries, `'''` multi-line values); `bruno.json` / `collection.bru` /
  `folder.bru` → `monica.yml` defaults (headers, vars) and `monica.js` hooks; environments with
  `vars:secret` as `${NAME}`; `seq` order; bearer / basic / API key auth with inheritance; all
  HTTP body modes (form files copied); scripts, `vars:*`, `assert` (as `test()` + Chai) and
  `tests` behind a `bru` / `req` / `res` shim (`lib/monica-bruno.js`, sharing
  `lib/monica-chai.js` with the Postman shim). The fixture collection imports and runs end
  to end (all requests pass) against the same stand-in API as the Postman fixture.

Shared for 9.2/9.3: a common `pkg/importer` (interface + `.mon`/`monica.yml` writer + report),
so each source only implements parsing.

---

## Dependency summary

| Dependency | Phase | Status |
|------------|-------|--------|
| `gopkg.in/yaml.v3` | 2 | ✅ direct |
| `github.com/dop251/goja` | 3 | ✅ approved, pinned to 2025-06-30 (Go 1.22 compatible) |
| `github.com/dop251/goja_nodejs` | 5 | ✅ approved (2026-09-18 commit; needs goja ≥ 2025-03) |
| `github.com/evanw/esbuild` | 5.5 | ✅ approved (v0.28.2, Go 1.13+) |
| `github.com/charmbracelet/bubbletea`, `bubbles`, `lipgloss` | 9.1 | ✅ approved (v2: `charm.land/bubbletea/v2` v2.1.1, `bubbles/v2` v2.2.1, `lipgloss/v2` v2.0.6) |

Postman and Bruno import need no new dependencies (stdlib JSON + hand-written `.bru` parser).

## Suggested order

`0.x → … → 9.x`: every phase is done.

1.3 landed before Phase 2 as parse-only. `%}` still can't appear inside a `{% %}` block;
the importers write such scripts to `.js` files instead.
