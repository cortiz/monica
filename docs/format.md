# Mon file format
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
  | `# @retry <count> [delay]` | Send a failed request again, up to `count` (1–100) more times, waiting `delay` (e.g. `1s`, default none) in between. |
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
* **Multipart bodies** — with a `Content-Type: multipart/…; boundary=…` header (written
  literally, not through a `{{variable}}`), the body is a list of parts written as raw HTTP:
  each part opens with a `--boundary` line, then its headers, a blank line and its content;
  a `--boundary--` line ends the body. Lines are sent with `\r\n` endings, as multipart
  requires, whatever the file uses. A part whose content is a single `< path` line is replaced
  by that file's contents, byte for byte (relative to the `.mon` file; `{{variables}}` are not
  expanded inside it); other content is text. The boundary parameter is required, the body
  must start with `--boundary` and end with `--boundary--` (blank lines around it aside), and
  each part needs the blank line after its headers; anything else is an input error
  (`file:line`). See [the example](#example).
* **Separator** — any line starting with `###` ends the current request and starts a new one,
  even inside a body. Text after the `###` is the request's title (used in logs and errors).
  Sections with no request line (only comments or blank lines) are ignored.
  A body that ends at a separator loses its trailing blank lines; the body of the last request
  in the file is kept verbatim.
* **Scripts** — JavaScript attached to a request (see [Scripting](scripting.md#scripting)).
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
  `--var name=value`, `vars.set()` in a script, the `--data` row, the selected
  environment's `vars`, then
  `defaults.vars` (see
  [Configuration](configuration.md#configuration)). An unknown variable is an input error (`file:line`).
  Built-ins: `{{$uuid}}` (random v4 UUID), `{{$timestamp}}` (Unix seconds),
  `{{$randomInt}}` (0–999). Values are not expanded again, there is no escape for a literal
  `{{`, and a body loaded with `< path` is sent as is.
* Both `\n` and `\r\n` line endings are accepted.

### Example
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
### Upload a file with a form field (multipart)
POST http://example.com/upload
Content-Type: multipart/form-data; boundary=MonicaBoundary

--MonicaBoundary
Content-Disposition: form-data; name="title"

Users for {{env}}
--MonicaBoundary
Content-Disposition: form-data; name="file"; filename="users.csv"
Content-Type: text/csv

< ./payloads/users.csv
--MonicaBoundary--
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
