# Interactive UI (`monica tui`)

```bash
monica tui                 # the .mon files here and up to 3 folders down
monica tui -e dev -r 5 --var token=abc
```

A full-screen terminal UI over the same runner as `monica run`:

```
monica · env dev · 2 sent
╭─────────────────────────╮╭───────────────────────────────────────────────╮
│▾ api/                   ││api/users.mon:3                                │
│  ▾ users.mon            ││### List users                                 │
│    ✓ GET  list          ││# @name list                                   │
│      POST Create        ││GET {{baseUrl}}/users                          │
│▾ health.mon             │╰───────────────────────────────────────────────╯
│  ✗ GET  https://…/health│╭───────────────────────────────────────────────╮
│                         ││1 Body │ 2 Headers │ 3 Timings │ 4 Tests │ 5 Log │
│                         ││✓ list  200 OK · 42ms · 1/1 tests              │
│                         ││{                                              │
│                         ││  "users": [ … ]                               │
╰─────────────────────────╯╰───────────────────────────────────────────────╯
```

![monica tui: tree, request and JSON response](images/tui/1-main.png)

![monica tui: live load test](images/tui/6-load-live.png)

More: [tests tab](images/tui/2-tests.png) · [variables](images/tui/3-variables.png) ·
[environments](images/tui/4-env.png) · [history](images/tui/5-history.png) ·
[load summary](images/tui/7-load-done.png).

* **Tree** — folders, `.mon` files and their requests in [run order](configuration.md#run-order), marked ✓/✗/-
  after a send. A file that doesn't parse is marked `!` and shows its error.
* **Request** — the selected request's section of the file, scripts included.
* **Response** — tabs for the body (JSON indented and colored), headers, timings (DNS,
  connect, TLS, waiting, receiving, sizes), tests (✓/✗ with messages) and the log
  (`console.log` and warnings, which would otherwise garble the screen).
* **One session** — sends share one runner, as in a single `monica run`: `vars.set` values,
  cookies and `beforeAll` hooks carry over between sends (send `login`, then `me`).
  Selecting another environment starts a new session. Secrets are masked everywhere.
* **Editing** — `o` opens the file in `$VISUAL` / `$EDITOR` (default `vi`) at the request's line;
  the tree reloads when the editor exits, and whenever a `.mon` file changes on disk.
* **Load test** — `L` runs `monica load` on the selected file with the `load:` settings from
  `monica.yml` and shows VUs, iterations, requests, rps (with a sparkline), p95 and failures
  live, then the summary and thresholds; `esc` stops it early.
* Everything here can also be done with `monica run` / `monica load` (e.g. `-s file --filter
  name`); the TUI needs a terminal and is not meant for CI.

| Key | Action |
|-----|--------|
| `↑`/`↓`, `j`/`k`, `pgup`/`pgdn`, `g`/`G` | Move in the tree |
| `←`/`→`, `h`/`l`, `space` | Fold / unfold a folder or file |
| `enter`, `s` | Send the selected request (`enter` on a folder or file folds it) |
| `esc`, `x` | Cancel the running request |
| `tab` | Switch focus between the tree and the response (arrows then scroll it) |
| `1`–`5`, `[`/`]` | Response tab: body, headers, timings, tests, log |
| `e` | Pick the environment |
| `v` | Variables the selected request would see, with their source (`--var`, `vars.set`, `data`, `config`) |
| `H` | History of this session's sends (`enter` shows one again) |
| `o` | Open the file in `$EDITOR` |
| `r` | Reload the files |
| `L` | Load-test the selected file |
| `?` | Help |
| `q`, `ctrl+c` | Quit |

| Flag | Meaning |
|------|---------|
| `-r, --recursive <n>` | Folder depth to list `.mon` files from (default 3) |
| `-e, --env <name>` | Environment to start with (default `$MONICA_ENV`) |
| `--var name=value`, `-c, --config`, `-k, --insecure`, `--no-cookies` | As for `monica run` |
| `--cert`, `--key`, `--cacert`, `--proxy` | As for `monica run` |

Colors follow the terminal's light or dark background (asked when the TUI starts; other
commands never query the terminal).
