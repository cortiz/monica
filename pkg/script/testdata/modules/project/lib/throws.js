function boom() {
  throw new Error("boom from module");
}
module.exports = { boom };
