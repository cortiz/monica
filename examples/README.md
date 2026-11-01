# Monica examples

Runnable `.mon` files, 2–3 per feature. They send requests to
[httpbin.org](https://httpbin.org), which echoes what it receives, so every
request has tests that check what was sent. Run them from this folder:

```bash
cd examples
monica run -r 3                       # everything (folders in name order)
monica run -s 01-basics/requests.mon  # one file
monica run -r 3 --var baseUrl=http://localhost:8080   # another httpbin-compatible server
```

| Folder | Files | Shows |
|--------|-------|-------|
| [`monica.yml`](monica.yml) | | Shared config: `baseUrl`, default headers, `envs`, `scripts.paths`, `load:` defaults |
| [`01-basics`](01-basics) | [requests](01-basics/requests.mon) · [methods](01-basics/methods.mon) · [variables](01-basics/variables.mon) | GET/POST/PUT/PATCH/DELETE, query strings, `@name`, `@timeout`, `@skip`, non-2xx statuses, config variables, `{{$uuid}}` / `{{$timestamp}}` / `{{$randomInt}}`, default headers |
| [`02-chaining`](02-chaining) | [login](02-chaining/login.mon) · [list-then-detail](02-chaining/list-then-detail.mon) · [response-headers](02-chaining/response-headers.mon) | `vars.set()` from a response body or header, reused in URLs, headers and bodies |
| [`03-cookies`](03-cookies) | [cookies](03-cookies/cookies.mon) · [delete-cookie](03-cookies/delete-cookie.mon) · [cookie-header](03-cookies/cookie-header.mon) | The run's cookie jar (try `--no-cookies`), expired cookies, explicit `Cookie` headers |
| [`04-upload`](04-upload) | [upload](04-upload/upload.mon) · [multiple-files](04-upload/multiple-files.mon) · [json-file](04-upload/json-file.mon) | Multipart forms with file parts, several files plus a JSON part, `< ./file` bodies |
| [`05-data-driven`](05-data-driven) | [create-user](05-data-driven/create-user.mon) · [create-product](05-data-driven/create-product.mon) · [login-matrix](05-data-driven/login-matrix.mon) | `--data` with CSV and JSON files, expected outcomes in the data |
| [`06-hooks`](06-hooks) | [hooked](06-hooks/hooked.mon) · [admin/](06-hooks/admin/admin.mon) | `monica.js` `beforeAll` / `beforeEach` / `afterEach`, nested folders, `req.skip()` in a hook |
| [`07-modules`](07-modules) | [signed](07-modules/signed.mon) · [builtins](07-modules/builtins.mon) · [esm-and-json](07-modules/esm-and-json.mon) | TypeScript and ES modules from `lib/`, CommonJS helpers, JSON files, `monica/crypto`, `encoding`, `random`, `jsonpath` |
| [`08-scripts`](08-scripts) | [pre-request](08-scripts/pre-request.mon) · [assertions](08-scripts/assertions.mon) · [script-files](08-scripts/script-files.mon) | Changing the request in a script, every `expect()` matcher, `console.log`, scripts in `.js` files |
| [`09-control`](09-control) | [skip](09-control/skip.mon) · [retry](09-control/retry.mon) · [filters](09-control/filters.mon) | `@skip`, conditional `req.skip()`, `@retry`, `--filter` and `--bail` |
| [`10-environments`](10-environments) | [envs](10-environments/envs.mon) · [secrets](10-environments/secrets.mon) | A folder's own `monica.yml`, `--env`, `${NAME}` secrets from `.env` (masked as `****`), `monica.local.yml` |
| [`11-load`](11-load) | [checkout](11-load/checkout.mon) | A user journey for `monica load`: VUs, stages, arrival rate, thresholds, JSON report |
| [`12-import`](12-import) | [README](12-import/README.md) | Sources for `monica import curl`, `postman` and `bruno` |

Commands the comments mention:

```bash
monica run -s 05-data-driven/create-user.mon --data 05-data-driven/users.csv
monica run -s 05-data-driven/create-product.mon --data 05-data-driven/products.json
monica run -s 05-data-driven/login-matrix.mon --data 05-data-driven/login-matrix.csv
monica run -s 10-environments/envs.mon --env staging
monica run -s 09-control/skip.mon --env staging --var smoke=1
monica run -r 3 --filter 'smoke-*'
monica load -s 11-load/checkout.mon -u 5 --duration 10s --threshold 'p95<500ms'
monica load -s 01-basics/requests.mon       # load: settings from monica.yml
monica tui                                  # browse and send them interactively
```

Be gentle with the public httpbin.org: point `baseUrl` at your own server for
anything bigger.

These files are run by the test suite (`pkg/cmd/examples_test.go`, against a
local stand-in for httpbin), so they stay in sync with Monica.
