# Importing (curl, Postman, Bruno, .http)

Convert what you already have into `.mon` files and a `monica.yml`.

* Turn a curl command (e.g. from the browser's "Copy as cURL") into a request
	```bash
	monica import curl 'curl https://api.example.com/users -H "Accept: application/json"'
	pbpaste | monica import curl -o users.mon      # read the command from stdin
	monica import curl -- curl -sS https://api.example.com/health   # already split by the shell
	```
	The `.mon` text goes to stdout, or to `-o, --out <file>` (an existing file is kept unless
	`-f, --force`); `--name <name>` sets `@name`. Quoting follows the shell: `'...'`, `"..."`,
	`$'...'`, backslash escapes and `\` line continuations (bash copies); a command with `^`
	line continuations or `^"` quotes is read as Windows `cmd` would (`^` escapes, `"..."`,
	`\"`), as in Chrome's "Copy as cURL (cmd)". Converted options:

	| curl | `.mon` |
	|------|--------|
	| `-X`, `--request` | method (otherwise `HEAD` with `-I`, `PUT` with `-T`, `POST` with data, else `GET`) |
	| `-H`, `--header` | header (`Name;` sends an empty value; `Name:` is ignored) |
	| `-d`, `--data`, `--data-ascii` | body; several are joined with `&`; adds `Content-Type: application/x-www-form-urlencoded` unless set |
	| `--data-raw`, `--data-binary`, `--data-urlencode` | body as is / URL-encoded (`name=value` encodes the value) |
	| `-d @file`, `--data-binary @file`, `-T file` | `< file` body; with `--out`, relative paths are rewritten to be relative to the `.mon` file's folder (curl reads them from the current directory) |
	| `--json` | body plus `Content-Type` and `Accept: application/json` |
	| `-G`, `--get`, `--url-query` | data appended to the URL query |
	| `-u user:pass`, `--oauth2-bearer` | `Authorization: Basic …` / `Bearer …` header |
	| `-A`, `-e`, `-b name=value` | `User-Agent`, `Referer`, `Cookie` headers |
	| `-F name=value`, `-F name=@file[;type=…][;filename=…]`, `-F name=<file`, `--form-string` | [multipart body](format.md#mon-file-format) (`< file` parts; a file's type is guessed from its extension) |
	| `-m`, `--max-time` | `# @timeout` |
	| several URLs | one request each, separated by `###` |

	A URL without a scheme gets `http://`, as in curl. Options that don't change the request
	(`-s`, `-v`, `-L`, `--compressed`, `-o`, …) are dropped; others (`-k`, `--proxy`, cookie
	files, unknown options) are dropped with a warning on stderr, as are shell variables
	(`$TOKEN`, kept as text) and anything after an unquoted `|`, `;`, `&`, `<` or `>`.
	`-H @file`, data from stdin, and what a `.mon` file can't hold as written (an `-F` value
	with a line break or one that starts with `< `, a body line starting with `###` or `> {%`,
	a body ending in blank lines before another URL's request) are errors. Text such as `{{x}}` is kept and
	warned about, since monica reads it as a variable.

* Import a Postman collection (export it as **Collection v2.1**), with its environments
	```bash
	monica import postman shop.postman_collection.json --env dev.postman_environment.json -o shop
	cd shop && monica run -r 3 --env shop-dev
	```
	* **Layout** — folders become directories and each request a `.mon` file (`### name`,
	  `# @name`), listed in `order:` so they run in collection order. Collection and folder
	  scripts become [`monica.js` hooks](scripting.md#folder-hooks-monicajs) (`beforeEach` / `afterEach`).
	* **Variables** — collection variables go to `defaults.vars`, each `--env` file to
	  `envs.<name>` (the environment's name as a slug). Values of type *secret* are never
	  written: they become `${NAME}` placeholders (listed in `.env.example`; `.env` is added to
	  `.gitignore`). `{{var}}` syntax is the same; `{{$guid}}` / `{{$randomUUID}}` become
	  `{{$uuid}}`, `{{$timestamp}}` and `{{$randomInt}}` stay, other dynamic variables are reported.
	* **Requests** — URLs (disabled query parameters dropped, `:path` variables filled), enabled
	  headers, bodies (raw with its content type, urlencoded, form-data with `< file` parts,
	  file, GraphQL as JSON) and auth: bearer, basic (computed in a pre-request script when it
	  uses variables), API key (header or query), OAuth 2.0's stored access token; auth is
	  inherited from folders and the collection. Other auth types are reported.
	* **Scripts** — pre-request and test scripts are copied into `< {% %}` / `> {% %}` blocks
	  (or `.js` files when they contain `%}`) behind `const pm = require("monica-postman")();`,
	  a compatibility module written to `lib/` (`scripts.paths`). It supports `pm.test`,
	  `pm.expect` (Chai BDD subset: `equal`, `eql`, `include`, `property` / `nested.property`,
	  `lengthOf`, `above` / `below` / `within`, `a` / `an`, `match`, `oneOf`, `keys`, `empty`,
	  `true` / `false` / `null` / `ok`, `not`, `deep`), `pm.response` (`code`, `status`,
	  `json()`, `text()`, `headers`, `to.have.status/header/jsonBody`, `to.be.ok/success/...`),
	  `pm.request.headers.add/upsert/remove` and `pm.environment` / `collectionVariables` /
	  `globals` / `variables` / `iterationData` (all monica `vars`). `pm.sendRequest`,
	  `setNextRequest`, the legacy `postman.*` / `tests[...]` API, `pm.cookies`, timers and
	  sandbox libraries get a `// TODO(monica-import):` line and a report entry.
	* **Report** — `IMPORT_REPORT.md` lists everything not converted or converted with changes
	  (secrets to set, files to copy from the author's machine, unsupported auth or script
	  features). The output folder must be empty unless `-f, --force`.

* Import a Bruno collection (the folder with `bruno.json`)
	```bash
	monica import bruno ./bruno/shop -o shop
	cd shop && monica run -r 3 --env dev
	```
	* **Layout** — folders and `.bru` requests become directories and `.mon` files, run in
	  Bruno's order (`seq`, then name; folders first) through `order:`. `collection.bru` headers
	  become `defaults.headers`, its `vars:pre-request` `defaults.vars`; its scripts and tests,
	  and those of each `folder.bru` (plus folder vars), become
	  [`monica.js` hooks](scripting.md#folder-hooks-monicajs). Folder headers are added to their requests.
	* **Environments** — `environments/*.bru` become `envs.<file name>`; `vars:secret` names have
	  no value in a Bruno collection, so they become `${NAME}` placeholders (listed in
	  `.env.example`). `{{process.env.X}}` becomes `{{X}}`, read from `${X}`. Dynamic variables map
	  as for Postman.
	* **Requests** — URL with `params:path` filled (and `params:query` added when the URL has no
	  query), enabled headers, bodies (`json`, `text`, `xml`, `sparql`, `form-urlencoded`,
	  `multipart-form` with `@file(...)` parts copied into the project, `graphql` + vars as
	  JSON) and auth with inheritance: bearer, basic, API key (header or query). Other auth
	  modes, non-HTTP requests (gRPC, WebSocket) and `docs` are not converted.
	* **Scripts** — `vars:pre-request`, `script:pre-request`, `vars:post-response`,
	  `script:post-response`, `assert` and `tests` become `< {% %}` / `> {% %}` blocks, each
	  part in its own `{ }` block behind
	  `const { bru, req, res, expect } = require("monica-bruno")();` (written to `lib/`).
	  `bru.getVar/setVar/getEnvVar/setEnvVar` use monica `vars`; `bru.interpolate`,
	  `bru.getEnvName`; `req.getUrl/setUrl/getMethod/setMethod/getHeader(s)/setHeader(s)/getBody/setBody`;
	  `res.status/headers/body/responseTime`, `res.getStatus/getHeader/getBody/...` and
	  `res("a.b[0]")`; `expect` is Chai-style (the same subset as the Postman module). `assert`
	  lines become `test()` calls (`eq`, `neq`, `gt(e)`, `lt(e)`, `in`, `notIn`, `contains`,
	  `notContains`, `length`, `matches`, `notMatches`, `startsWith`, `endsWith`, `between`,
	  `isEmpty`, `isNotEmpty`, `isNull`, `isUndefined`, `isDefined`, `isTruthy`, `isFalsy`,
	  `isJson`, `isNumber`, `isString`, `isBoolean`, `isArray`). `bru.runRequest`,
	  `bru.sendRequest`, `bru.setNextRequest`, `bru.sleep`, `bru.getProcessEnv`, timers and npm
	  libraries get a `// TODO(monica-import):` line and a report entry.
	* **Report** — `IMPORT_REPORT.md`, as for Postman. The output folder must be empty unless
	  `-f, --force`, and can't be the collection folder.

* Import JetBrains HTTP Client / VS Code REST Client files (`.http`, `.rest`): one file, or a
  folder searched recursively (hidden folders and `node_modules` skipped)
	```bash
	monica import http requests.http -o api
	monica import http ./http -o api        # keeps the folder structure
	cd api && monica run -r 3 --env dev
	```
	Each `.http` file becomes a `.mon` file at the same place (body, multipart and script files
	it references are copied next to it, so relative paths keep working).

	| `.http` | Monica |
	|---------|--------|
	| `###` separator and title, `#` / `//` comments | the same (`### title`, `#` comments) |
	| `# @name x` / `// @name x` (or the `###` title) | `# @name` |
	| `# @timeout 5` (seconds, or `ms` / `s` / `m`) | `# @timeout 5s` |
	| `# @no-redirect` | `defaults.followRedirects: false` when every request has it; otherwise reported |
	| `# @prompt x` | `{{x}}` read from `${X}` (`.env.example`) |
	| `METHOD URL [HTTP/x]`, URL alone (`GET`), indented `?a=1` / `&b=2` continuation lines | request line (version dropped); `/path` + `Host:` becomes an absolute URL, no scheme gets `http://` |
	| headers, body, `< ./file` body, multipart parts with `< ./file` | the same; `<@ ./file` (with variables) is inlined |
	| `GRAPHQL` requests, `X-Request-Type: GraphQL` | JSON `POST` with `query` and `variables` |
	| `@x = value` file variables (VS Code, JetBrains) | `defaults.vars` (renamed `<file>_x` when another file or an environment uses the name); a value that refers to other variables is written out where it is used |
	| `http-client.env.json` (`$shared` → `defaults`), next to the files or in a parent folder up to the repository root | `envs.<name>.vars`; nested objects become dotted names (`{{auth.user}}`) |
	| `http-client.private.env.json` | `${NAME}` placeholders (`${ENV_NAME}` when several environments have the key), listed in `.env.example`; values are never written |
	| `.vscode/settings.json` `rest-client.environmentVariables` (`$shared`, `{{$shared x}}`, references between values) and `rest-client.defaultHeaders` | `envs` / `defaults.vars` (references resolved at import) / `defaults.headers` |
	| `{{$uuid}}`, `{{$random.uuid}}`, `{{$guid}}`, `{{$timestamp}}`, `{{$randomInt}}` | monica built-ins |
	| `{{$processEnv X}}`, `{{$dotenv X}}`, `{{$env.X}}` | `{{X}}`, read from `${X}` |
	| `{{$isoTimestamp}}`, `{{$timestamp -1 d}}`, `{{$datetime iso8601\|rfc1123\|"fmt" [offset]}}`, `{{$localDatetime ...}}`, `{{$randomInt 1 10}}`, `{{$random.integer(a, b)}}` / `.float` / `.alphabetic(n)` / `.alphanumeric(n)` / `.hexadecimal(n)` / `.email` | computed in a pre-request script (`dynamic()` from `lib/monica-http.js`) into `{{_dynamicN}}` |
	| VS Code request variables `{{login.response.body.$.token}}`, `{{login.response.headers.X-Id}}` | a post-response script on `login` does `vars.set("login_token", ...)` (JSONPath via `monica/jsonpath`); the reference becomes `{{login_token}}` |
	| `< {% %}` / `< ./pre.js` pre-request scripts, `> {% %}` / `> ./handler.js` response handlers | `< {% %}` / `> {% %}` blocks or `.js` files behind `const { client, request, response, jsonPath } = require("monica-http")();` |

	The `lib/monica-http.js` module (`scripts.paths`) supports `client.test`, `client.assert`,
	`client.log`, `client.global.set/get/clear/clearAll/isEmpty` (monica `vars`),
	`response.status`, `response.body` (parsed when the content type is JSON, else text),
	`response.headers.valueOf/valuesOf`, `response.contentType.mimeType/charset`,
	`request.method`, `request.url/body.getRaw()/tryGetSubstituted()`,
	`request.headers.all()/findByName()`, `request.variables.set/get`,
	`request.environment.get` and `jsonPath(value, path)`. `import` statements are moved
	above the script's block and relative modules copied.

	Not converted (each gets a `TODO(monica-import)` comment and a report entry):
	`>>` / `>>!` response output redirection, `client.exit()`, streaming handlers
	(`onEachLine` / `onEachMessage`), the scripts' `crypto` and `$random` APIs (use
	`monica/crypto` / `monica/random`), npm packages, `WEBSOCKET` / `GRPC` / `ws://` requests,
	`@no-cookie-jar`, `@connection-timeout` and other directives, `HTTP/2` forcing, XPath
	request variables, `SSLConfiguration` / `Security` / `$proxy` env settings, other dynamic
	variables (`{{$random.address.city}}`, `{{$aadToken}}`, ...), and files outside the
	imported folder. `@no-log` and `@note` are dropped. The output folder must be empty unless
	`-f, --force`, and can't be the folder of the `.http` files.
