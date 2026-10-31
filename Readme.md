# Monica

Monica is designed to handle Network requests and responses. 

## Features
* Parse raw HTTP requests and return a Request object.
* Send HTTP requests with custom headers and body.
* Parse HTTP responses, including headers and body.

## Usage
* Runs all `mon` files in the current directory
	```bash
	monica run 
	```
* Run a single file 
	```bash
	monica run -s <file>
	```

Responses are printed to stdout; logs and errors go to stderr.

### Exit codes
| Code | Meaning |
|------|---------|
| `0`  | All requests succeeded |
| `1`  | At least one request failed (e.g. connection error) |
| `2`  | Usage or input error (unknown flag, missing file, invalid `.mon` syntax) |

Parse errors are reported as `file:line: message`.

## Mon file format
```
# Comment
METHOD URL [HTTP/x.y]
Header-Name: value
Header-Name: another value

BODY

### Optional title
METHOD URL
...
```

A file holds one or more requests, separated by `###` lines. Requests run in file order;
a failing request does not stop the ones after it.

* **Comments** — lines starting with `#` before the body are ignored (before the request
  line and between headers). Blank lines before the request line are ignored too.
* **Request line** — `METHOD URL`, optionally followed by an HTTP version such as `HTTP/1.1`.
  The method must be a valid HTTP token; the URL must be absolute with an `http`/`https`
  scheme and a host.
* **Headers** — one `Name: value` per line, split on the first `:` only, so values may contain
  colons (`X-Foo: a: b`). Spaces around the value are trimmed. Repeated headers keep every
  value. Header names are case-insensitive.
* **Body** — everything after the first blank line, kept verbatim (including lines starting
  with `#` and trailing newlines). With no blank line the request has no body.
* **Separator** — any line starting with `###` ends the current request and starts a new one,
  even inside a body. Text after the `###` is the request's title (used in logs and errors).
  Sections with no request line (only comments or blank lines) are ignored.
  A body that ends at a separator loses its trailing blank lines; the body of the last request
  in the file is kept verbatim.
* Both `\n` and `\r\n` line endings are accepted.

#### Example
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
GET http://example.com/users
Accept: application/json

### Create user
POST http://example.com/users
Content-Type: application/json

{"name": "ada"}
```
