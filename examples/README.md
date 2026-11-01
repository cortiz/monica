# Monica examples

Runnable `.mon` files, one feature per folder. They send requests to
[httpbin.org](https://httpbin.org), which echoes what it receives, so every
request has tests that check what was sent. Run them from this folder:

```bash
cd examples
monica run -r 3                       # everything (folders in name order)
monica run -s 01-basics/requests.mon  # one file
monica run -r 3 --var baseUrl=http://localhost:8080   # another httpbin-compatible server
```

| Folder | Shows |
|--------|-------|
| [`monica.yml`](monica.yml) | Shared config: `baseUrl`, default headers, `scripts.paths`, `load:` defaults |
| [`01-basics`](01-basics/requests.mon) | GET with a query, POST JSON with `{{$uuid}}`, `@name`, `@timeout`, `@skip`, `test()` / `expect()` |
| [`02-chaining`](02-chaining/login.mon) | Storing a response value with `vars.set()` and sending it as a bearer token |
| [`03-cookies`](03-cookies/cookies.mon) | The run's cookie jar (try `--no-cookies`) |
| [`04-upload`](04-upload/upload.mon) | A multipart form with a file part, and a whole file as the body (`< ./files/report.csv`) |
| [`05-data-driven`](05-data-driven/create-user.mon) | One run per CSV row: `monica run -s 05-data-driven/create-user.mon --data 05-data-driven/users.csv` |
| [`06-hooks`](06-hooks/monica.js) | Folder hooks in `monica.js`: `beforeAll`, `beforeEach`, `afterEach` |
| [`07-modules`](07-modules/signed.mon) | `import` from a TypeScript module in `lib/`, a CommonJS helper, `monica/crypto` and `monica/jsonpath` |

Load-test the basics with the `load:` settings from `monica.yml` (5 VUs for 10s,
with thresholds):

```bash
monica load -s 01-basics/requests.mon
```

Be gentle with the public httpbin.org: point `baseUrl` at your own server for
anything bigger.

These files are run by the test suite (`pkg/cmd/examples_test.go`, against a
local stand-in for httpbin), so they stay in sync with Monica.
