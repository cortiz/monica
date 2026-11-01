// Shared test helpers: require("checks") from any script (scripts.paths).
exports.ok = () =>
  test("status is 2xx", () => {
    expect(res.status).toBeGreaterThanOrEqual(200);
    expect(res.status).toBeLessThan(300);
  });

exports.echoed = (path, value) =>
  test(`echoes ${path}`, () => expect(res.json()).toHaveProperty(path, value));
