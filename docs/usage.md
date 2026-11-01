# Usage
* Start a project: `monica.yml`, an `example.mon` and `.gitignore` entries
	```bash
	monica init               # in the current directory, or: monica init ./api
	monica run -s example.mon
	```
	`monica.yml` sets a `baseUrl` variable (`--base-url`, default `https://httpbin.org`) in
	`defaults` and a `dev` environment; `example.mon` holds a GET and a POST request with
	tests. `.env` and `monica.local.yml` are appended to `.gitignore` when it doesn't list
	them. Existing `monica.yml` and `example.mon` files are kept unless `-f, --force`; each
	file is reported as `created`, `overwritten`, `updated` or `skipped`.

* Run all `.mon` files in the current directory (add `-r 3` to include subfolders)
	```bash
	monica run
	```
* Run a single file
	```bash
	monica run -s <file>
	```
* Run only some requests, stop at the first failure, without response bodies
	```bash
	monica run --filter 'login' --filter 'users-*' --bail -q
	```
	Requests skipped by `--filter` don't run their scripts either, so variables they would set
	(`vars.set`) are not available to later requests.

* Run everything once per row of a data file (data-driven run)
	```bash
	monica run --data users.csv
	```
	The CSV's first line names the variables (`user,pass`); each later line is one iteration.
	A `.json` file is an array of objects (`[{"user": "ada", "id": 7}]`): strings are used as
	is, `null` as an empty value, numbers, booleans, arrays and objects as their JSON text. Each
	row runs every selected file in run order, with the row's values as `{{variables}}`;
	`monica.iteration` is the row number (1-based). A `vars.set` value for one of the row's
	names is dropped when the next row starts; other `vars.set` values carry over. Folder
	`beforeAll` hooks run once, before the first row that needs them, and `afterAll` at the
	end. Reports name the row (`(api.mon:3, row 2)`), and `--bail` stops all later rows. A
	missing, empty or invalid data file is a usage error.

* Pick an environment, override variables, or use a specific config file
	```bash
	monica run --env dev --var token=abc --var id=7 --config ./monica.yml
	```

| Flag | Meaning |
|------|---------|
| `-s, --single <file>` | Run one `.mon` file |
| `-r, --recursive <n>` | Directory depth to search for `.mon` files (default 1) |
| `-e, --env <name>` | Environment from `monica.yml` (default `$MONICA_ENV`) |
| `--var name=value` | Set a variable; repeatable, overrides `monica.yml` |
| `-c, --config <file>` | Use this config instead of the nearest `monica.yml` |
| `--reporter <format>` | `console` (default), `json` or `junit` |
| `-o, --out <file>` | Write the report to a file |
| `--bail` | Stop at the first failed request (later requests and files are not run) |
| `--filter <glob>` | Only run requests whose `@name`, `###` title or `METHOD URL` matches; `*` matches any text, `?` one character; repeatable |
| `-q, --quiet` | Hide response headers and bodies in the console report |
| `--data <file>` | Run once per row of a `.csv` or `.json` file, with the row's values as variables |
| `-k, --insecure` | Skip TLS certificate verification (as `insecureSkipVerify: true` in every config) |
| `--cert <file>`, `--key <file>` | Client certificate and its private key (PEM) for mutual TLS; set both; override `tls.clientCert`/`tls.clientKey` |
| `--cacert <file>` | Extra CA certificates (PEM) trusted besides the system ones; overrides `tls.caCert` |
| `--proxy <url>` | Proxy (`http://`, `https://` or `socks5://`), or `none` to ignore `HTTP(S)_PROXY`; overrides `proxy` |
| `--no-cookies` | Don't keep cookies between requests |

**Cookies** — a run keeps one cookie jar: cookies a response sets (`Set-Cookie`) are sent on
later requests to matching hosts and paths, in the same file or later ones (and later
`--data` rows), as a browser would. A `Cookie` header written in a request is sent as well.
`--no-cookies` turns the jar off. Under `monica load` each VU has its own jar.

The report goes to stdout; logs and errors go to stderr. For each request it prints a status
line, the response, and ✓/✗ per test (with the failure message), then a summary:

```
✓ login  POST https://api.example.com/login → 200 OK  42ms  (api.mon:3)

HTTP/1.1 200 OK
Content-Type: application/json
{"token":"****"}

    ✓ returns a token
✗ users  GET https://api.example.com/users → 200 OK  18ms  (api.mon:12)
    ✗ has ada
        expected values to be deeply equal
          expected: "ada"
          received: "bob"
- slow  skipped: flaky upstream  (api.mon:20)

3 requests: 1 passed, 1 failed, 1 skipped · 2 tests: 1 passed, 1 failed · 61ms
```

**CI reports** — `--reporter json` or `--reporter junit` writes a machine-readable report. With
`--out <file>` it goes to the file and the console report still prints to stdout; without
`--out` only the machine report is printed, so it can be piped.

```bash
monica run --reporter junit --out monica-report.xml
```

* **JSON**: `{"summary": {...}, "requests": [{"file", "line", "name", "method", "url",
  "status", "statusText", "durationMs", "iteration" (with `--data`), "attempts" (with `@retry`), "skipped", "skipReason", "error", "failed",
  "tests": [{"name", "passed", "error"}]}]}`. Response bodies are not included.
* **JUnit**: one `<testsuite>` per `.mon` file; each `test()` is a `<testcase>` (a request
  without tests is one testcase). Failed tests are `<failure>`, request or script errors
  `<error>`, `@skip` requests `<skipped>`. With `--data`, testcase classnames end in `[row N]`.

## Exit codes
| Code | Meaning |
|------|---------|
| `0`  | All requests succeeded |
| `1`  | At least one request failed (e.g. connection error, a script threw or timed out) |
| `2`  | Usage or input error (unknown flag, missing file, invalid `.mon` syntax or config, unknown variable or environment, script syntax error) |

Parse errors are reported as `file:line: message`.

## Validate without sending (`monica validate`)
`monica validate` checks the files `monica run` would run, with the same selection flags
(`-s`, `-r`, `-e`, `--var`, `-c`, `--filter`, `--data`, and `--cert`, `--key`, `--cacert`,
`--proxy`), but sends nothing and runs no script.
Use it in CI or a pre-commit hook.

```bash
monica validate -r 3                 # every .mon file up to 3 folders deep
monica validate -s api.mon -e staging
```

It reports:
* `.mon` syntax errors, including missing body (`< ./file`) and script (`> ./tests.js`) files;
* an invalid `monica.yml` (or `monica.local.yml`, `.env`) and an unknown `--env`;
* TLS certificate files that can't be read or parsed and invalid proxy URLs, from `tls:` /
  `proxy:` and the flags (the files are read; nothing is dialed);
* JavaScript/TypeScript syntax errors in inline `{% %}` blocks, script files and the
  `monica.js` hooks that apply (compiled, not run);
* `{{variables}}` that nothing defines, in URLs, headers, bodies and default headers;
* as **warnings**, `${NAME}` secrets missing from the environment and `.env`.

Variables set at run time can't be known up front, so a variable counts as defined when
`monica.yml` or `--var` sets it, when it is a column of the `--data` file, or when any script
of the run (request scripts, the `monica.js` hooks that apply, modules in `scripts.paths`)
contains `vars.set("name"` with that literal name (`"`, `'` or `` ` `` quotes). The setters of
imported projects count too: `pm.environment.set`, `pm.collectionVariables.set`,
`pm.variables.set`, `pm.globals.set`, `bru.setVar`, `bru.setEnvVar`, `client.global.set` and
`request.variables.set`. Names built at
run time (`vars.set(key, ...)`) are not detected; pass them with `--var` if needed.

It prints one line per problem, then a summary:

```
login.mon:9: SyntaxError: Unexpected token ;
users.mon:4: listUsers: unknown variable "tokne"
monica.yml:6: warning: ${API_TOKEN} is not set (in the environment or .env)
3 problems
```

Without errors it ends with `N files, M requests OK` (and the number of warnings, if any).
Exit codes: `0` no errors (warnings allowed), `2` at least one error or a usage error.

## Shell completion
`monica completion bash|zsh|fish|powershell` prints a completion script. Besides commands and
flags, it completes `-e/--env` with the environments of the nearest `monica.yml` (or `-c`),
`-s/--single` with `.mon` files, `--data` with `.csv`/`.json` files and `--reporter` with its
formats.

```bash
# bash (needs the bash-completion package)
monica completion bash > ~/.local/share/bash-completion/completions/monica
# zsh (compinit enabled; any folder on $fpath)
monica completion zsh > "${fpath[1]}/_monica"
# fish
monica completion fish > ~/.config/fish/completions/monica.fish
```

Start a new shell afterwards; `monica completion <shell> --help` shows more options.
