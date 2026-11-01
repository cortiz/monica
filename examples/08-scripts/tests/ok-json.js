// A post-response script file with reusable checks.
test("status 2xx", () => expect(res.status).toBeLessThan(300));
test("JSON response", () => expect(res.headers.get("Content-Type")).toContain("json"));
test("stamp echoed", () => expect(res.json().headers["X-Stamp"]).toBe(vars.get("lastStamp")));
