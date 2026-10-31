# Monica

Monica is designed to handle Network requests and responses. 

## Features
* Parse raw HTTP requests and return a Request object.
* Send HTTP requests with custom headers and body.
* Parse HTTP responses, including headers and body.

## Usage
* Runs all `mon` files in the current directory
	```bash
	monica run 
	```
* Run a single file 
	```bash
	monica run -s <file>
	```

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

### Exit codes
| Code | Meaning |
|------|---------|
| `0`  | All requests succeeded |
| `1`  | At least one request failed (e.g. connection error, a script threw or timed out) |
| `2`  | Usage or input error (unknown flag, missing file, invalid `.mon` syntax or config, unknown variable or environment, script syntax error) |

Parse errors are reported as `file:line: message`.

## Scripting

Scripts are JavaScript (ES5.1 plus most of ES2015+, via [goja](https://github.com/dop251/goja))
in a sandbox: no `require`, file system, network, timers or `process`. Each script runs in its
own function scope; share data between requests with `vars`.

**Order per request:** pre-request script → `{{variable}}` expansion and default headers →
send → print response → post-response script.

| Global | Pre-request | Post-response |
|--------|-------------|---------------|
| `req.method`, `req.url`, `req.body` | read/write (before `{{}}` expansion) | read-only (as sent) |
| `req.headers.get(n)` / `.has(n)` / `.all()` | ✓ | ✓ |
| `req.headers.set(n, v)` / `.add(n, v)` / `.delete(n)` | ✓ | throws |
| `res.status`, `res.statusText`, `res.body`, `res.time` (ms) | – | ✓ |
| `res.headers.get(n)` (values joined by `, `, `null` if absent) / `.has(n)` / `.all()` | – | ✓ |
| `res.json()` (throws on invalid JSON) | – | ✓ |
| `vars.get(n)` (`undefined` if unset) / `vars.has(n)` / `vars.set(n, v)` | ✓ | ✓ |
| `monica.env` (selected environment or `""`) | ✓ | ✓ |
| `console.log/info/warn/error` (to the log, secrets masked) | ✓ | ✓ |
| `test(name, fn)`, `expect(value)` | ✓ | ✓ |

`vars.set` stores the value as a string; it is visible to every later request of the run,
including those in later files, but `--var` still wins.

**Tests** — `test(name, fn)` runs `fn` and records a pass, or a failure when it throws; the
other tests still run. Any failed test fails its request (exit 1). `expect(value)` matchers
(each also available as `expect(value).not.<matcher>`):

| Matcher | Passes when |
|---------|-------------|
| `toBe(x)` | `value` is `x` (`Object.is`: same primitive or same object) |
| `toEqual(x)` | deeply equal (arrays and plain objects compared by content) |
| `toContain(x)` | string contains `x`, or array has an element deeply equal to `x` |
| `toMatch(re)` | string matches the RegExp `re` (or contains the string `re`) |
| `toBeLessThan(n)`, `toBeLessThanOrEqual(n)`, `toBeGreaterThan(n)`, `toBeGreaterThanOrEqual(n)` | numeric comparison |
| `toHaveProperty("a.b.0"[, x])` | the own-property path exists (and deeply equals `x`) |
| `toHaveLength(n)` | `value.length === n` |
| `toBeTruthy()`, `toBeFalsy()`, `toBeNull()`, `toBeUndefined()`, `toBeDefined()` | as named |

A failed `expect` outside `test()` fails the whole script.

```
GET {{baseUrl}}/users/1

> {%
  test("returns user", () => {
    expect(res.status).toBe(200);
    expect(res.json().name).toEqual("ada");
    expect(res.time).toBeLessThan(500);
  });
%}
```

**Errors** point at the line in the script's own file: the `.mon` file for `{% %}` blocks, the
`.js` file for script files, e.g. `api.mon:12:5: TypeError: Cannot read property 'id' of
undefined`. A pre-request script that throws or times out stops its request from being sent; a
post-response script that throws fails the request (exit 1). A script that doesn't compile is
an input error (exit 2). Each run is limited by `scripts.timeout` (default 5s).

```
POST {{baseUrl}}/login
Content-Type: application/json

{"user": "ada"}

> {% vars.set("token", res.json().token) %}

###
GET {{baseUrl}}/me
Authorization: Bearer {{token}}
```

## Configuration

`monica.yml` holds settings and variables shared by `.mon` files. For each `.mon` file Monica
uses the nearest `monica.yml` in its directory or any parent directory. Without one, the
built-in defaults apply (30s timeout, redirects followed, TLS verified).

```yaml
version: 1                  # required
defaults:                   # apply to every environment
  timeout: 30s              # Go duration: 500ms, 5s, 1m
  followRedirects: true
  insecureSkipVerify: false # true accepts self-signed certificates
  headers:                  # added when a request doesn't set them
    User-Agent: monica/0.2.0
  vars:
    apiVersion: v2
envs:                       # each env merges over defaults
  dev:
    vars:
      baseUrl: http://localhost:8080
      pass: ${DEV_PASSWORD}
  prod:
    timeout: 10s
    vars:
      baseUrl: https://api.example.com
scripts:
  timeout: 5s               # per script run (default 5s)
  paths: [./lib]            # reserved for script modules (not used yet)
load:                       # reserved for load testing (not used yet)
  vus: 50
  duration: 30s
  thresholds: ["p95<300ms", "errors<1%"]
```

* Unknown keys, a missing or unsupported `version` and invalid durations are errors that name
  the file (and line when known).
* **Environments** — the selected environment's settings merge over `defaults`; `headers` and
  `vars` merge key by key.
* **`${NAME}`** in header and variable values is read from the OS environment, then from a
  `.env` file next to `monica.yml` (`KEY=VALUE` lines, `#` comments, optional `export` and
  quotes). An unset name is an error. Use quotes in flow-style YAML (`{ pass: "${X}" }`).
* **Secret masking** — every value substituted for a `${NAME}` is printed as `****` in
  responses, error messages and logs (the request itself is sent with the real value). Values
  are masked wherever they appear, so a short or common value (e.g. `true`) masks those
  characters everywhere; `--var` values are not masked.
* **`monica.local.yml`** next to `monica.yml` deep-merges on top of it (`version` optional).
  Keep it and `.env` out of version control for personal or secret values.
* Default header values may use `{{name}}`; variable values are used literally.

## Mon file format
```
# Comment
# @name requestName
< {% /* pre-request script */ %}
METHOD URL [HTTP/x.y]
Header-Name: value
Header-Name: another value

BODY

> {% /* post-response script */ %}

### Optional title
METHOD URL
...
```

A file holds one or more requests, separated by `###` lines. Requests run in file order;
a failing request does not stop the ones after it.

* **Comments** — lines starting with `#` before the body are ignored (before the request
  line and between headers). Blank lines before the request line are ignored too.
* **Directives** — comment lines of the form `# @key [value]` set request metadata. They must
  come before the request line (a directive among the headers is an error). Each may appear
  once per request; unknown directives are errors, so comments starting with `# @` are reserved.

  | Directive | Meaning |
  |-----------|---------|
  | `# @name <name>` | Names the request (letters, digits, `_`, `-`, `.`; not starting with a digit). Unique within a file. Shown in logs and errors; used for chaining later. |
  | `# @timeout <duration>` | Per-request timeout, e.g. `500ms`, `5s`, `1m`. Overrides the default (30s). |
  | `# @skip [reason]` | Parse the request but don't send it. |
* **Request line** — `METHOD URL`, optionally followed by an HTTP version such as `HTTP/1.1`.
  The method must be a valid HTTP token; the URL must be absolute with an `http`/`https`
  scheme and a host.
* **Headers** — one `Name: value` per line, split on the first `:` only, so values may contain
  colons (`X-Foo: a: b`). Spaces around the value are trimmed. Repeated headers keep every
  value. Header names are case-insensitive.
* **Body** — everything after the first blank line, kept verbatim (including lines starting
  with `#` and trailing newlines). With no blank line the request has no body.
* **Body from file** — a body that is a single line `< path` (a space or tab after `<`) is
  replaced by the contents of that file, byte for byte. Relative paths are resolved from the
  `.mon` file's directory. A missing file is an input error (`file:line`). Any other body,
  including one with more lines after the `< path` line, is sent literally.
* **Separator** — any line starting with `###` ends the current request and starts a new one,
  even inside a body. Text after the `###` is the request's title (used in logs and errors).
  Sections with no request line (only comments or blank lines) are ignored.
  A body that ends at a separator loses its trailing blank lines; the body of the last request
  in the file is kept verbatim.
* **Scripts** — JavaScript attached to a request (see [Scripting](#scripting)).
  * **Pre-request** — `< {% ... %}` before the request line (with comments and directives, in
    any order).
  * **Post-response** — `> {% ... %}` after the headers or after the body. It ends the body
    (trailing blank lines dropped); after it, only comments and blank lines may follow until
    the next `###`. A body line starting with `>` that is not `> {%` is ordinary body text.
  * A block may be one line (`< {% code %}`) or span lines up to the first `%}`; inside a block
    every line is code, including lines starting with `#` or `###`. Nothing but spaces may
    follow `%}`, and `%}` cannot appear inside the code.
  * **Script files** — `< ./pre.js` / `> ./tests/users.js` load the script from a file instead
    (a single path ending in `.js`; relative paths resolve from the `.mon` file's directory).
    A missing file is an input error. In a body, `>` lines that are not `> {%` or a `.js`
    path stay body text.
  * At most one pre-request and one post-response script per request (inline or file).
* **Variables** — `{{name}}` in the URL, header values and body is replaced by a variable's
  value (spaces inside the braces are allowed: `{{ name }}`). Values come from, highest first:
  `--var name=value`, `vars.set()` in a script, the selected environment's `vars`, then
  `defaults.vars` (see
  [Configuration](#configuration)). An unknown variable is an input error (`file:line`).
  Built-ins: `{{$uuid}}` (random v4 UUID), `{{$timestamp}}` (Unix seconds),
  `{{$randomInt}}` (0–999). Values are not expanded again, there is no escape for a literal
  `{{`, and a body loaded with `< path` is sent as is.
* Both `\n` and `\r\n` line endings are accepted.

#### Example
```
# Simple HTTP Request
GET http://httpbin.org/status/200
Host: httpbin.org
User-Agent: monica/0.1.0

```

```
# Simple POST HTTP Request
POST http://httpbin.org/anything
Host: httpbin.org
User-Agent: monica/0.1.0

{
  "key": "value"
}
```

```
### List users
# @name listUsers
# @timeout 5s
GET http://example.com/users
Accept: application/json

### Create user
POST http://example.com/users
Content-Type: application/json

{"name": "ada"}
```

```
### Create user from a payload file
POST http://example.com/users
Content-Type: application/json

< ./payloads/user.json
```

```
### Login
# @name login
< {%
  vars.set("ts", Date.now());
%}
POST http://example.com/login
Content-Type: application/json

{"user": "ada"}

> {%
  test("logged in", () => expect(res.status).toBe(200));
%}
```

```
### List users, tests in a file
GET http://example.com/users

> ./tests/users.js
```

```
### Templated request (with monica.yml above)
GET {{baseUrl}}/{{apiVersion}}/users/{{ id }}
X-Request-Id: {{$uuid}}
```
