# Configuration

`monica.yml` holds settings and variables shared by `.mon` files. For each `.mon` file Monica
uses the nearest `monica.yml` in its directory or any parent directory. Without one, the
built-in defaults apply (30s timeout, redirects followed, TLS verified).

```yaml
version: 1                  # required
defaults:                   # apply to every environment
  timeout: 30s              # Go duration: 500ms, 5s, 1m
  followRedirects: true
  insecureSkipVerify: false # true accepts self-signed certificates
  tls:                      # PEM files, relative to this file; ${NAME} allowed
    caCert: certs/corp-ca.pem       # trusted in addition to the system CAs
    # clientCert: certs/client.pem  # mutual TLS: set clientCert and clientKey together
    # clientKey: certs/client-key.pem
  proxy: http://proxy.corp:3128     # or socks5://host:port; none (or "") = no proxy
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
    tls:
      clientCert: certs/prod.pem
      clientKey: ${PROD_KEY_FILE}
    proxy: none
    vars:
      baseUrl: https://api.example.com
scripts:
  timeout: 5s               # per script run (default 5s)
  paths: [./lib]            # folders for require("name"), inside monica.yml's folder
order: [auth/login.mon, users]  # run these first (see Run order)
load:                       # defaults for monica load
  vus: 50
  duration: 30s             # or stages (not both):
  # stages:                 #   ramp step by step (see Load testing)
  #   - {duration: 30s, target: 50}
  #   - {duration: 1m, target: 0}
  # rate: 100/s             # start iterations at a fixed rate instead
  thresholds: ["p95<300ms", "errors<1%"]   # pass/fail conditions (see Load testing)
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
* **TLS** — `tls.caCert` adds CA certificates to the system ones (no `insecureSkipVerify`
  needed for a private CA); `tls.clientCert` and `tls.clientKey` send a client certificate
  (mutual TLS) and must be set together (one without the other is an error). Paths are
  relative to the file that sets them; `${NAME}` is expanded. An environment, or
  `monica.local.yml`, that sets `clientCert`/`clientKey` replaces both; `caCert` is replaced
  on its own. Files are read once per run, when the config is loaded and before its
  requests are sent (flag files before anything runs); a missing or invalid file is a config
  error (exit 2). `--cert`, `--key` and `--cacert` override them.
* **Proxy** — without `proxy`, the `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` environment
  variables apply (loopback addresses are never proxied). `proxy: http://host:port` (also
  `https://` and `socks5://`, optionally with `user:password@`) sends every request through
  that proxy; `proxy: none` or `proxy: ""` connects directly, ignoring the environment.
  `--proxy` overrides it. A proxy password is masked like a secret.

## Run order

`monica run` runs files in a fixed order: sorted by path, folder by folder and by name at each
level (so `a/x.mon` runs before `a.mon`, which runs before `b/y.mon`). Requests run in file
order. To run some files first, list them in `order:` in `monica.yml`: each entry is a
`.mon` file or a folder, relative to the config's folder. Files matching an entry run first,
in list order (the first matching entry counts; a folder's files keep path order); the rest
follow in path order. `order` applies to the files that config governs; with several
`monica.yml` files, each orders its own files. An entry that places no file is logged as a
warning. `monica.local.yml` replaces the whole list.
