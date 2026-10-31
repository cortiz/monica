// Module state lives as long as the engine: every require() gets this object.
let n = 0;
module.exports = { next: () => ++n };
