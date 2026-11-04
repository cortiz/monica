# Load testing

`monica load` runs the same `.mon` files with several virtual users (VUs) at once:

```bash
monica load --vus 10 --duration 30s      # 10 VUs for 30 seconds
monica load -u 5 -i 1000 -s checkout.mon # 1000 iterations shared by 5 VUs
monica load --stage 30s:50 --stage 1m:50 --stage 30s:0   # ramp 0 → 50 VUs, hold, ramp down
monica load --rate 100/s --duration 1m -u 50             # 100 iterations/s on up to 50 VUs
monica load --rate 10/s --stage 1m:200 -u 100            # rate ramps 10 → 200/s
```

```
monica load: 10 VUs, 30s
iterations  1234 completed (41.1/s), 2 interrupted
requests    2468 (82.2/s), 3 failed (0.12%)
data        1.2 MB received, 48.0 kB sent
duration    30.02s

request  reqs  failed  rps   min     avg     p50     p90     p95     p99     max
login    1234  0       41.1  8.10ms  12.4ms  11.9ms  16.2ms  18.0ms  24.5ms  61.3ms
users    1234  3       41.1  2.05ms  4.31ms  3.98ms  6.10ms  7.02ms  11.8ms  40.2ms
all      2468  3       82.2  2.05ms  8.36ms  7.40ms  14.9ms  16.8ms  22.1ms  61.3ms
phases (avg / p95)  dns 0 / 0 · connect 95µs / 410µs · tls 0 / 0 · waiting 7.90ms / 16.1ms · receiving 310µs / 920µs
```

![monica load: summary and thresholds](images/load/cli-summary.png)

While it runs, a progress line is redrawn on stderr ([screenshot](images/load/cli-live.png)); the
[TUI](tui.md#interactive-ui-monica-tui) has a live dashboard for the same test.

* **Metrics** — one row per request (by file and line, named by its `@name`, title or
  `METHOD URL`) and an `all` row: count, failures, requests per second and the request time
  (send until the body is read) as min, average, p50/p90/p95/p99 and max. Percentiles come
  from a histogram with under 1.6% error; min, max and average are exact. `phases` breaks
  the time down: DNS lookup, TCP connect and TLS handshake (zero on a kept-alive connection),
  waiting (request sent → first response byte) and receiving (the body). `data` counts
  request and response bodies. Requests that got no response (e.g. connection refused) count
  as requests and failures but not in the timings; requests aborted by the stop are left out.

* **Iteration** — one pass over every selected file in [run order](configuration.md#run-order) (`-s`, `-r`
  and `--filter` work as for `monica run`; `-e`, `--var` and `-c` too). Each VU has its own
  script runtime and variables, so `vars.set` values stay with that VU from one iteration to
  the next; scripts see `monica.vu` and `monica.iteration`. Folder `beforeAll` hooks run once
  per VU, before its first iteration, and `afterAll` when it stops.
* **Modes** — by default `--vus` VUs loop over the files. `--stage duration:target`
  (repeatable, or `load.stages`) ramps the VU count linearly to each target in turn,
  starting from `--vus` (default 0). The count is rounded to the nearest VU (on a ramp from
  0 to 10 over 10s, VU 1 joins at 0.5s); a VU above the current count finishes its iteration
  and waits, and VUs are only started once the ramp reaches them. The stages' total is the test's
  duration. `--rate` (or `load.rate`: `100`, `100/s`, `6000/m`, `90/h`, `1/500ms`) starts
  iterations on a fixed schedule instead, however long each takes, on up to `--vus` VUs
  (default 100; created as needed). An iteration due while every VU is busy is dropped and
  counted (`5 dropped (no free VU)`): raise `--vus` if that happens. With `--rate` and
  stages, the targets are iterations per second and the rate ramps from `--rate`.
  `load.vus` stands in for `--vus` in every mode.
* **Limits** — `--duration` (or the end of the stages) stops new iterations; iterations still
  running get `--graceful-stop` (default 30s) to finish, then their requests are aborted and
  they are counted as interrupted. `-i, --iterations` is a total shared by all VUs; with a
  duration or stages, whichever comes first. Defaults come from `load:` in `monica.yml`
  (`--config` or the nearest one above the current directory), and flags win: `--duration`
  replaces `load.stages`, `--stage` replaces `load.duration`, and `--iterations` alone ignores
  `load.duration`. `--stage` and `--duration` together are an error. With no duration, stages
  or iterations anywhere, 1 VU runs 1 iteration.
* **Thresholds** — pass/fail conditions checked at the end, from `load.thresholds` in
  `monica.yml` or `--threshold` flags (which replace the config list):

  ```bash
  monica load -u 20 --duration 1m --threshold 'p95<300ms' --threshold 'errors<1%' \
    --threshold 'login:p(99.9)<1s' --threshold 'rps>=100'
  ```

  Each is `[request:]metric op value` with `op` one of `<`, `<=`, `>`, `>=`. Metrics:
  `min`, `avg`, `med`, `max` and percentiles `p95` / `p(99.9)` (compared with a duration such
  as `300ms`), `errors` (failed share of requests: `1%` or `0.01`) and `rps` (requests per
  second). `request:` limits it to the requests with that label (`@name`, title or
  `METHOD URL`); without it, all requests count. A threshold on requests that never ran (or
  never got a response, for latency metrics) fails. Results are printed after the summary
  (`✓ p95<300ms  p95=120ms`); an invalid threshold is a usage error.
* **Failures and exit codes** — a request fails as in `monica run` (connection error, script
  error, failed `test()`); `@skip`ped requests are not counted. Failed requests alone don't
  change the exit code; a failed threshold exits with `1`. Input errors (invalid file or
  config, unknown variable, an iteration with no requests left after `--filter`/`@skip`)
  stop the test with exit code `2`, and Ctrl-C prints the summary (and thresholds) so far
  with exit code `1`.
* **Live progress** — while the test runs, a line on stderr is redrawn every second when
  stderr is a terminal (`-q` turns it off):
  `running 12s/30s · VUs 8/10 · 1234 iterations · 2468 requests · 82.0 rps · p95 18.0ms · 0.12% failed`
  (active/started VUs; rps and p95 over the last second).
* **JSON report** — `-o, --out report.json` also writes the results as JSON; `--out -` prints
  the JSON on stdout instead of the text summary, for piping. It holds the `config` the test
  ran with, `durationMs`, `vus`, `iterations`, `interrupted`, `dropped`, `aborted` (Ctrl-C),
  `total` and one entry per request in `requests` (`name`, `file`, `line`, `requests`,
  `failed`, `failRate`, `rps`, `bytesSent`, `bytesReceived`, `latency` {`count`, `min`,
  `avg`, `med`, `p90`, `p95`, `p99`, `max`} and `phases` {`dns`, `connect`, `tls`, `waiting`,
  `receiving`: {`avg`, `p95`}}), `thresholds` [{`threshold`, `passed`, `actual`}] and
  `passed`. Times are milliseconds.
* **Connections and cookies** — all VUs share one connection pool that keeps an idle
  (kept-alive) connection per VU and host, so connections are reused instead of reopened;
  `--no-keepalive` opens a new connection for every request (the `connect`/`tls` phases then
  show the handshake cost). Each VU has its own cookie jar: cookies a response sets are sent
  on that VU's later requests (to matching hosts), never another VU's, so a VU that logs in
  keeps its session. `-k, --insecure` skips TLS certificate verification.
* Only warnings and errors are logged (`console.log` output too), unless `--debug`.

| Flag | Meaning |
|------|---------|
| `-u, --vus <n>` | Virtual users running at once (default `load.vus`, else 1); with `--stage` the count to ramp from (default 0), with `--rate` the most at once (default 100) |
| `--duration <d>` | Stop starting iterations after this long, e.g. `30s` (default `load.duration`) |
| `-i, --iterations <n>` | Total iterations, shared by all VUs |
| `--stage <d:target>` | Ramp step, e.g. `30s:50`; repeatable, replaces `load.stages`; not with `--duration` |
| `--rate <n/period>` | Start iterations at a fixed rate, e.g. `100/s` (default `load.rate`) |
| `--graceful-stop <d>` | Time running iterations get at the end of `--duration` or the stages (default `30s`) |
| `-o, --out <file>` | Also write a JSON report; `-` prints it on stdout instead of the text summary |
| `-q, --quiet` | No live progress line |
| `--no-keepalive` | New connection for every request |
| `-k, --insecure` | Skip TLS certificate verification |
| `--cert`, `--key`, `--cacert`, `--proxy` | As for `monica run` (certificate files are read once and shared by all VUs) |
| `--threshold <cond>` | Pass/fail condition, e.g. `p95<300ms`, `login:errors<1%`; repeatable, replaces `load.thresholds` |
| `-s`, `-r`, `-e`, `--var`, `-c`, `--filter` | As for `monica run` |
