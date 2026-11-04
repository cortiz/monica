# Monica

Monica runs **HTTP requests written as plain-text `.mon` files**, checks the responses with
**JavaScript tests**, and **load-tests** the same files. It is a single binary: no Node.js,
no account, no GUI required (though there is a [terminal UI](tui.md#interactive-ui-monica-tui)).

```
### Get a user
GET https://httpbin.org/get?name=ada
Accept: application/json

> {% test("status is 200", () => expect(res.status).toBe(200)) %}
```

Keep `.mon` files next to your code, review them in pull requests, and run them in CI.


## Features
* `.mon` files: raw HTTP requests with `{{variables}}`, directives (`@name`, `@timeout`,
  `@retry`, `@skip`), body files and multipart uploads.
* `monica.yml`: environments, default headers, `${SECRET}` values from the environment or
  `.env` (masked in output), run order.
* Pre-request and post-response scripts with `test()` / `expect()`, shared modules
  (CommonJS, ES modules, TypeScript), folder hooks and built-in crypto/encoding/JSONPath.
* Chaining with `vars.set()`, a cookie jar per run, data-driven runs (`--data` CSV/JSON).
* Console, JSON and JUnit reports; exit codes for CI.
* `monica validate`: check syntax, configs, scripts and variables without sending anything.
* Shell completion for bash, zsh, fish and PowerShell (`monica completion`), including
  environment names and `.mon` files.
* `monica load`: virtual users, ramping stages, arrival rates, percentiles, thresholds.
* `monica tui`: browse, send and load-test requests in an interactive terminal UI.
* `monica init` to start a project; `monica import curl|postman|bruno|http` to bring existing requests over (JetBrains HTTP Client and VS Code REST Client `.http` files included).

## Where next

* [Install](install.md) Monica, then follow the five-minute [quick start](quick-start.md).
* [Running requests](usage.md): `monica run`, reports, `monica validate` and shell completion.
* [The `.mon` format](format.md) and [configuration](configuration.md) (`monica.yml`, environments, TLS, proxy).
* [Scripting and tests](scripting.md), [load testing](load-testing.md) and the [terminal UI](tui.md).
* [Importing](importing.md) from curl, Postman, Bruno and `.http` files.
* Runnable [examples](https://github.com/cortiz/monica/tree/main/examples), one feature per folder.
