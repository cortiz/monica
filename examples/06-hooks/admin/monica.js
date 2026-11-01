// Nested hooks: these run inside the parent folder's (06-hooks/monica.js).
// beforeEach order is outer → inner; afterEach is inner → outer.
module.exports = {
  beforeAll() {
    vars.set("adminToken", "admin-" + require("monica/random").string(8));
  },
  beforeEach() {
    req.headers.set("Authorization", "Bearer " + vars.get("adminToken"));
    // Skip requests marked as destructive unless running against staging.
    if (req.headers.has("X-Destructive") && monica.env !== "staging") {
      req.skip("destructive: only on staging");
    }
  },
  afterEach() {
    test("admin call answered", () => expect(res.status).toBeLessThan(400));
  },
};
