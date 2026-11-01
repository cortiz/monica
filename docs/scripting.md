# Scripting

Scripts are JavaScript (ES5.1 plus most of ES2015+, via [goja](https://github.com/dop251/goja))
in a sandbox: no file system, network, timers or `process`; `require()` loads only the
project's own modules (see [Modules](#modules)). Each script runs in its own function scope;
share data between requests with `vars`.

**Order per request:** `beforeEach` [hooks](#folder-hooks-monicajs) → pre-request script →
`{{variable}}` expansion and default headers → send → print response → post-response script →
`afterEach` hooks.

**Skipping and retrying** — `req.skip("reason")` in a pre-request script or `beforeEach` hook
skips the request (reported as skipped, not a failure): it is not sent, and later
`beforeEach` hooks, the pre-request script, the post-response script and `afterEach` hooks
don't run. With `# @retry <count> [delay]`, a request that fails (connection error, timeout,
a script or hook throwing, a failed `test()`) runs again from `beforeEach`, up to `count`
more times; only the last attempt is reported, with its attempt count. An HTTP error status
alone is not a failure, so to retry on `5xx` add a test such as
`test("ok", () => expect(res.status).toBeLessThan(500))`. Input errors (unknown variable,
script syntax error) and `req.skip()` are not retried; scripts rerun on each attempt, so
`vars.set` effects add up.

```
# @retry 3 1s
< {% if (monica.env !== "staging") req.skip("staging only") %}
GET {{baseUrl}}/jobs/{{jobId}}
> {% test("done", () => expect(res.json().state).toBe("done")) %}
```

| Global | Pre-request | Post-response |
|--------|-------------|---------------|
| `req.method`, `req.url`, `req.body` | read/write (before `{{}}` expansion) | read-only (as sent) |
| `req.headers.get(n)` / `.has(n)` / `.all()` | ✓ | ✓ |
| `req.headers.set(n, v)` / `.add(n, v)` / `.delete(n)` | ✓ | throws |
| `req.skip([reason])` (don't send the request; the script still runs to its end) | ✓ | throws |
| `res.status`, `res.statusText`, `res.body`, `res.time` (ms) | – | ✓ |
| `res.headers.get(n)` (values joined by `, `, `null` if absent) / `.has(n)` / `.all()` | – | ✓ |
| `res.json()` (throws on invalid JSON) | – | ✓ |
| `vars.get(n)` (`undefined` if unset) / `vars.has(n)` / `vars.set(n, v)` | ✓ | ✓ |
| `monica.env` (selected environment or `""`) | ✓ | ✓ |
| `monica.iteration` (`--data` row, 1-based; under `monica load`, the VU's iteration; else `0`) | ✓ | ✓ |
| `monica.vu` (virtual user under `monica load`, 1-based; else `0`) | ✓ | ✓ |
| `console.log/info/warn/error` (to the log, secrets masked) | ✓ | ✓ |
| `test(name, fn)`, `expect(value)` | ✓ | ✓ |
| `require(path)`, `import` statements ([Modules](#modules)) | ✓ | ✓ |

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
`.js`/`.ts` file for script files, e.g. `api.mon:12:5: TypeError: Cannot read property 'id' of
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

## Modules

`require()` loads CommonJS modules (`module.exports` / `exports`), so helpers can be shared
between scripts:

```js
// lib/auth.js
exports.bearer = (token) => "Bearer " + token;
```
```
< {% req.headers.set("Authorization", require("auth").bearer(vars.get("token"))) %}
GET {{baseUrl}}/me
```

or, as ES modules / TypeScript:

```ts
// lib/auth.ts
export const bearer = (token: string): string => `Bearer ${token}`;
```
```
< {%
  import { bearer } from "auth";
  req.headers.set("Authorization", bearer(vars.get("token")));
%}
GET {{baseUrl}}/me

> ./tests/me.ts
```

* **Files** — `./x` and `../x` are relative to the requiring file (the `.mon` file for
  `{% %}` blocks, the `.js` file for script files and modules). `x`, `x.js`, `x.json` and
  `x/index.js` are tried in that order; `.json` files are parsed.
* **Bare names** (`require("auth")`) are looked up in the `scripts.paths` folders of
  `monica.yml` (relative to it), then in `node_modules` folders from the requiring file's
  folder up to the project root, Node-style: `package.json` `main`, `index.js`, subpaths
  (`require("pkg/lib/x")`) and scoped packages (`@scope/pkg`).
* **npm packages** — install with `npm install` next to `monica.yml`. Only pure-JavaScript
  CommonJS (or UMD) packages work: e.g. `lodash`, `dayjs`, `ajv`, `crypto-js`. Packages that
  need Node.js core modules (`fs`, `http`, `crypto`, …) such as `axios` or `uuid` fail with
  `Node.js core modules are not available`.
* **ES modules and TypeScript** — `.ts`, `.mts`, `.cts` and `.mjs` files, and `.js` files that
  use `import`/`export` statements, are transpiled to CommonJS when loaded (with
  [esbuild](https://esbuild.github.io), built in). Types are stripped, not checked. Error
  positions stay the original file's lines. `require("./x")` and `import "./x.js"` also find
  `x.ts` (and `index.ts` in a folder). `{% %}` blocks and script files (`> ./tests/me.ts`) may
  use `import` statements too; `export` makes no sense there. A `require()` of an ES module returns its exports
  (`default` holds the default export); `import x from "pkg"` of a CommonJS package gets
  `module.exports`. Top-level `await` is not supported. A large ES module package is slower
  to load the first time in a run (e.g. ~1s for all of `lodash-es`): import its single-function
  files (`lodash-es/debounce.js`) when that matters.
* **Sandbox** — only files under the project root (the folder of `monica.yml`, or of the
  `.mon` file when there is none) can be loaded; anything else, including `../` traversal,
  absolute paths and symlinks pointing out, fails with an error. `scripts.paths` folders must
  be inside the project root too (so a cloned project can't open up your file system).
* A module runs once per run: every `require()` of it returns the same `exports`, so module
  state carries over between requests. Module code counts against `scripts.timeout`, and its
  errors are reported at the `require()` call (with the module's own `file:line` for syntax
  errors).

**Built-in modules** (implemented in Go, always available; strings are UTF-8):

| Module | Exports |
|--------|---------|
| `monica/crypto` | `sha256(data[, enc])`, `sha512`, `sha1`, `md5`, `hmac(alg, key, data[, enc])` with `alg` one of `sha256`, `sha512`, `sha1`, `md5`; `enc` is `hex` (default), `base64` or `base64url` |
| `monica/encoding` | `base64Encode/Decode`, `base64UrlEncode/Decode` (unpadded; decoding accepts both), `hexEncode/Decode`, `urlEncode/Decode` (form encoding: space ↔ `+`) |
| `monica/random` | `uuid()` (v4), `int(min, max)` (inclusive), `string(length[, alphabet])` (default `A-Za-z0-9`); all from a cryptographic source |
| `monica/jsonpath` | `query(value, path)` → array of matches, `value(value, path)` → first match or `undefined`; `value` may be a JSON string |

JSONPath supports `$`, `.name`, `['name']`, `[0]`, `[-1]`, `[*]`, `.*`, `..name` (recursive),
`[start:end]` and unions (`[0,2]`, `['a','b']`); filters (`[?()]`) are not supported. `..`
skips cycles, and a query that visits more than a million values throws.

```
< {%
  const { hmac } = require("monica/crypto");
  req.headers.set("X-Signature", hmac("sha256", vars.get("apiSecret"), req.body));
%}
POST {{baseUrl}}/orders
Content-Type: application/json

{"id": 1}

> {%
  const jp = require("monica/jsonpath");
  vars.set("orderId", jp.value(res.body, "$.order.id"));
  test("all items priced", () => expect(jp.query(res.json(), "$.items[*].price")).not.toContain(null));
%}
```

## Folder hooks (`monica.js`)

A `monica.js` module in a folder exports hooks for every request in that folder and below:

```js
// api/monica.js
const { uuid } = require("monica/random");

module.exports = {
  beforeAll() { vars.set("runId", uuid()); },
  beforeEach() { req.headers.set("X-Run-Id", vars.get("runId")); },
  afterEach() { test("no server error", () => expect(res.status).toBeLessThan(500)); },
  afterAll() { console.log("done with", vars.get("runId")); },
};
```

* **Which files** — the `monica.js` in the `.mon` file's folder and in each parent folder up to
  the project root (the folder of `monica.yml`; without one, only the `.mon` file's folder).
  Outer hooks run first for `before*`, last for `after*`.
* **Per request** — `beforeEach` → pre-request script → `{{}}` expansion → send →
  post-response script → `afterEach`. `beforeEach` sees `req` like a pre-request script (and
  can change it); `afterEach` sees `req` and `res` like a post-response script, and runs even
  when the post-response script failed. `test()` results are added to the request.
* **Once per run** — a folder's `beforeAll` runs before the first request under it that is
  actually run (not `@skip`ped or filtered out); `afterAll` runs at the end of the run, in
  reverse order. They see `vars`, `monica`, `console`, `require` and `expect`; `test()` is not
  available there.
* **Failures** — a `monica.js` that doesn't load, or whose `beforeAll` throws, is reported as
  a failed result and every request under it is skipped. A throwing `afterAll` is reported as
  a failed result; one in `beforeEach` fails the request without sending it, one in
  `afterEach` fails the request. Each hook call has the `scripts.timeout` budget; `async`
  functions are not awaited.
* The hooks module is a regular module: it can `require()` others, and scripts that
  `require("./monica.js")` get the same instance.
