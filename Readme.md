# Monica

Monica runs **HTTP requests written as plain-text `.mon` files**, checks the responses with
**JavaScript tests**, and **load-tests** the same files. It is a single binary: no Node.js,
no account, no GUI required (though there is a [terminal UI](https://cortiz.github.io/monica/tui/)).

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

## Install

Pick one; each gives you a `monica` command. Check it with `monica --version`.

* **Binary** (Linux, macOS, Windows, FreeBSD) — download the archive for your system from the
  [releases page](https://github.com/cortiz/monica/releases), unpack it and put `monica` on
  your `PATH`:
  ```bash
  # Linux x86_64 shown; the others are monica_<Os>_<arch>.tar.gz (Darwin, Linux, Freebsd;
  # x86_64, arm64, armv6, riscv64) and monica_Windows_<arch>.zip
  VERSION=v1.0.0-rc.2
  curl -LO https://github.com/cortiz/monica/releases/download/$VERSION/monica_Linux_x86_64.tar.gz
  tar -xzf monica_Linux_x86_64.tar.gz monica
  sudo mv monica /usr/local/bin/
  ```
  Each release has a `checksums.txt` (`sha256sum -c checksums.txt --ignore-missing`).
  Archives also include this Readme and the [examples](examples/README.md). On macOS, a
  binary downloaded with a browser is quarantined; clear it with `xattr -d com.apple.quarantine monica`.
* **Homebrew** (macOS, from v1.0.0):
  ```bash
  brew install cortiz/tap/monica
  ```
  Upgrade with `brew upgrade monica`.
* **Docker** (linux/amd64 and linux/arm64) — the image's entrypoint is `monica` and its
  working directory `/work`, so mount your project there:
  ```bash
  docker run --rm -v "$PWD:/work" ghcr.io/cortiz/monica:1.0.0-rc.2 run -r 3
  ```
  Tags: `1.2.3` and `1.2` for each release, `latest` for the newest stable one; release
  candidates (`1.0.0-rc.2`) only get their own tag.
* **From source** — with Go 1.27+, clone the repository and run `make build`; the binary is
  `./monica`.

Requests without a `User-Agent` header send `monica/<version>`.

## Documentation

The full documentation is at **[cortiz.github.io/monica](https://cortiz.github.io/monica/)**:

* [Quick start](https://cortiz.github.io/monica/quick-start/): a five-minute tour
* [Running requests](https://cortiz.github.io/monica/usage/): `monica run`, reports, exit codes, `monica validate`, shell completion
* [`.mon` file format](https://cortiz.github.io/monica/format/)
* [Configuration](https://cortiz.github.io/monica/configuration/): `monica.yml`, environments, secrets, TLS and proxy, run order
* [Scripting & tests](https://cortiz.github.io/monica/scripting/): `test()` / `expect()`, modules, folder hooks
* [Load testing](https://cortiz.github.io/monica/load-testing/)
* [Terminal UI](https://cortiz.github.io/monica/tui/)
* [Importing](https://cortiz.github.io/monica/importing/): curl, Postman, Bruno and `.http` files

Runnable [examples](examples/README.md) cover one feature per folder. The site is built from
[`docs/`](docs/) with MkDocs (`make docs` serves it locally).
