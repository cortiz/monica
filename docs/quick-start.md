# Quick start

A five-minute tour. The requests go to [httpbin.org](https://httpbin.org), a public service
that echoes back what it receives.

**1. Create a project.** `monica init` writes a `monica.yml` (settings and variables) and an
`example.mon` with two requests:

```bash
mkdir my-api && cd my-api
monica init
monica run
```

```
✓ getExample  GET https://httpbin.org/get?source=monica → 200 OK  312ms  (example.mon:4)
    ✓ returns 200
✓ postExample  POST https://httpbin.org/post → 200 OK  158ms  (example.mon:12)
    ✓ echoes the body

2 requests: 2 passed, 0 failed · 2 tests: 2 passed, 0 failed · 471ms
```

`monica run` also prints each response; add `-q` to hide them.

**2. Write a request.** A `.mon` file is what you would send over the wire: a request line,
headers, a blank line and a body. `{{baseUrl}}` comes from `monica.yml`. A `> {% ... %}` block
after the request runs once the response arrives. Save this as `users.mon`:

```
### Create a user
# @name createUser
POST {{baseUrl}}/post
Content-Type: application/json

{"name": "ada"}

> {%
  test("created", () => expect(res.status).toBe(200));
  vars.set("userName", res.json().json.name);   // keep a value for later requests
%}

### Look the user up
# @name getUser
GET {{baseUrl}}/get?name={{userName}}

> {% test("found ada", () => expect(res.json().args.name).toBe("ada")) %}
```

```bash
monica run -s users.mon -q
```

`###` separates requests, which run top to bottom; `vars.set` passes data from one to the next.

**3. Use environments and secrets.** Add an environment to `monica.yml` and pick it with
`--env`; `${NAME}` values are read from the shell or a `.env` file and masked as `****` in the
output:

```yaml
envs:
  prod:
    vars:
      baseUrl: https://api.example.com
      token: ${PROD_TOKEN}
```

```bash
monica run --env prod          # or override one value: --var baseUrl=http://localhost:8080
```

**4. Run it in CI.** The exit code is `0` when every request passed, `1` when any failed;
`--reporter junit --out report.xml` writes a report your CI can show.

**5. Load-test it.** The same file, with 10 virtual users for 30 seconds, failing if the 95th
percentile is above 500ms:

```bash
monica load -s users.mon --vus 10 --duration 30s --threshold 'p95<500ms'
```

**6. Explore interactively.** `monica tui` opens a terminal UI to browse the requests, send
them one at a time, and inspect responses and variables.

Next: the [examples](https://github.com/cortiz/monica/blob/main/examples/README.md) (one folder per feature), [scripting](scripting.md#scripting)
(`test`, `expect`, `vars`, modules), and the full [`.mon` format](format.md#mon-file-format).
Coming from curl, Postman, Bruno or `.http` files? See [Importing](importing.md#importing-curl-postman-bruno-http).
