// Folder hooks: run for every request in this folder (and below).
const { uuid } = require("monica/random");

module.exports = {
  beforeAll() {
    vars.set("runId", uuid());
  },
  beforeEach() {
    req.headers.set("X-Run-Id", vars.get("runId"));
  },
  afterEach() {
    test("no server error", () => expect(res.status).toBeLessThan(500));
  },
};
