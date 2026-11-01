// An ES module (.mjs): import/export work in scripts and modules; it is
// transpiled to CommonJS when loaded.
export const money = (n, currency = "EUR") => `${n.toFixed(2)} ${currency}`;
export default function slug(s) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
}
