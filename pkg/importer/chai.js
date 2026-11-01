// Chai-style assertions (expect(x).to.be.a("string")...) shared by the Postman
// and Bruno compatibility modules written by `monica import`.
"use strict";

function show(v) {
  if (typeof v === "string") return JSON.stringify(v);
  try {
    var s = JSON.stringify(v);
    return s === undefined ? String(v) : s;
  } catch (e) {
    return String(v);
  }
}

function deepEqual(a, b) {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  var ka = Object.keys(a), kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (var i = 0; i < ka.length; i++) {
    if (!Object.prototype.hasOwnProperty.call(b, ka[i]) || !deepEqual(a[ka[i]], b[ka[i]])) return false;
  }
  return true;
}

function typeName(v) {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  return typeof v;
}

function isResponse(v) {
  return v !== null && typeof v === "object" && v.__monicaResponse === true;
}

// Assertion is a Chai-style chain: pm.expect(x).to.be.an("array").that.is.not.empty
function Assertion(value) {
  this._v = value;
  this._not = false;
  this._deep = false;
  this._nested = false;
}

Assertion.prototype._assert = function (ok, message, negated) {
  if (this._not) {
    ok = !ok;
    message = negated;
  }
  if (!ok) throw new Error(message);
  return this;
};

["to", "be", "been", "is", "that", "which", "and", "has", "have", "with", "at", "of", "same", "does", "but", "also", "still", "own"].forEach(function (w) {
  Object.defineProperty(Assertion.prototype, w, { get: function () { return this; } });
});
Object.defineProperty(Assertion.prototype, "not", { get: function () { this._not = !this._not; return this; } });
Object.defineProperty(Assertion.prototype, "deep", { get: function () { this._deep = true; return this; } });
Object.defineProperty(Assertion.prototype, "nested", { get: function () { this._nested = true; return this; } });

function flag(name, test, what) {
  Object.defineProperty(Assertion.prototype, name, {
    get: function () {
      var v = this._v;
      return this._assert(test(v), "expected " + show(v) + " to be " + what, "expected " + show(v) + " not to be " + what);
    },
  });
}
flag("true", function (v) { return v === true; }, "true");
flag("false", function (v) { return v === false; }, "false");
flag("null", function (v) { return v === null; }, "null");
flag("undefined", function (v) { return v === undefined; }, "undefined");
flag("NaN", function (v) { return v !== v; }, "NaN");
flag("exist", function (v) { return v !== null && v !== undefined; }, "defined");
flag("empty", function (v) {
  if (typeof v === "string" || Array.isArray(v)) return v.length === 0;
  if (v && typeof v === "object") return Object.keys(v).length === 0;
  return false;
}, "empty");
Object.defineProperty(Assertion.prototype, "ok", {
  get: function () {
    var v = this._v;
    if (isResponse(v)) return this._assert(v.code >= 200 && v.code < 300, "expected response to be ok, got " + v.code, "expected response not to be ok, got " + v.code);
    return this._assert(!!v, "expected " + show(v) + " to be truthy", "expected " + show(v) + " to be falsy");
  },
});
["success", "accepted", "badRequest", "unauthorized", "forbidden", "notFound", "rateLimited", "serverError", "clientError", "info", "redirection", "error"].forEach(function (name) {
  var ranges = {
    success: [200, 299], info: [100, 199], redirection: [300, 399], clientError: [400, 499], serverError: [500, 599], error: [400, 599],
    accepted: [202, 202], badRequest: [400, 400], unauthorized: [401, 401], forbidden: [403, 403], notFound: [404, 404], rateLimited: [429, 429],
  }[name];
  Object.defineProperty(Assertion.prototype, name, {
    get: function () {
      var c = this._v && this._v.code;
      return this._assert(c >= ranges[0] && c <= ranges[1], "expected response to be " + name + ", got " + c, "expected response not to be " + name + ", got " + c);
    },
  });
});

Assertion.prototype.equal = Assertion.prototype.equals = Assertion.prototype.eq = function (x) {
  var v = this._v;
  var ok = this._deep ? deepEqual(v, x) : v === x;
  return this._assert(ok, "expected " + show(v) + " to equal " + show(x), "expected " + show(v) + " not to equal " + show(x));
};
Assertion.prototype.eql = Assertion.prototype.eqls = function (x) {
  var v = this._v;
  return this._assert(deepEqual(v, x), "expected " + show(v) + " to deeply equal " + show(x), "expected " + show(v) + " not to deeply equal " + show(x));
};
Assertion.prototype.include = Assertion.prototype.includes = Assertion.prototype.contain = Assertion.prototype.contains = function (x) {
  var v = this._v, ok = false;
  if (typeof v === "string") ok = v.indexOf(x) >= 0;
  else if (Array.isArray(v)) ok = v.some(function (e) { return deepEqual(e, x); });
  else if (v && typeof v === "object" && x && typeof x === "object") {
    ok = Object.keys(x).every(function (k) { return deepEqual(v[k], x[k]); });
  }
  return this._assert(ok, "expected " + show(v) + " to include " + show(x), "expected " + show(v) + " not to include " + show(x));
};
Assertion.prototype.property = function (name, value) {
  var v = this._v, cur = v, ok = true;
  var parts = this._nested ? String(name).replace(/\[(\d+)\]/g, ".$1").split(".") : [name];
  for (var i = 0; i < parts.length; i++) {
    if (cur === null || cur === undefined || !(Object(cur) instanceof Object) || !(parts[i] in Object(cur))) { ok = false; break; }
    cur = cur[parts[i]];
  }
  if (ok && arguments.length > 1) ok = this._deep ? deepEqual(cur, value) : cur === value;
  var what = "property " + show(name) + (arguments.length > 1 ? " of " + show(value) : "");
  this._assert(ok, "expected " + show(v) + " to have " + what, "expected " + show(v) + " not to have " + what);
  if (arguments.length < 2 && !this._not) this._v = cur;
  return this;
};
// length / lengthOf work as a call (.lengthOf(2)) and as a chain
// (.length.above(1)), like Chai's chainable methods.
["length", "lengthOf"].forEach(function (name) {
  Object.defineProperty(Assertion.prototype, name, {
    get: function () {
      var self = this, v = this._v, len = v === null || v === undefined ? undefined : v.length;
      var chained = new Assertion(len);
      chained._not = this._not;
      var fn = function (n) {
        return self._assert(len === n, "expected " + show(v) + " to have length " + n + " but got " + len, "expected " + show(v) + " not to have length " + n);
      };
      Object.setPrototypeOf(fn, chained);
      return fn;
    },
  });
});
["all", "any", "contain", "itself"].forEach(function (w) {
  if (!Object.getOwnPropertyDescriptor(Assertion.prototype, w)) {
    Object.defineProperty(Assertion.prototype, w, { get: function () { return this; } });
  }
});
Assertion.prototype.members = function (list) {
  var v = this._v;
  var ok = Array.isArray(v) && v.length === list.length && list.every(function (x) { return v.some(function (e) { return deepEqual(e, x); }); });
  return this._assert(ok, "expected " + show(v) + " to have the same members as " + show(list), "expected " + show(v) + " not to have the same members as " + show(list));
};
Assertion.prototype.ownProperty = Assertion.prototype.haveOwnProperty = function (name) {
  var v = this._v, ok = v !== null && v !== undefined && Object.prototype.hasOwnProperty.call(Object(v), name);
  return this._assert(ok, "expected " + show(v) + " to have own property " + show(name), "expected " + show(v) + " not to have own property " + show(name));
};
Assertion.prototype.string = function (s) {
  var v = this._v;
  return this._assert(typeof v === "string" && v.indexOf(s) >= 0, "expected " + show(v) + " to contain " + show(s), "expected " + show(v) + " not to contain " + show(s));
};
Assertion.prototype.closeTo = Assertion.prototype.approximately = function (n, delta) {
  var v = this._v;
  return this._assert(Math.abs(v - n) <= delta, "expected " + show(v) + " to be close to " + n + " ± " + delta, "expected " + show(v) + " not to be close to " + n + " ± " + delta);
};
Assertion.prototype.instanceOf = Assertion.prototype.instanceof = function (C) {
  var v = this._v;
  return this._assert(v instanceof C, "expected " + show(v) + " to be an instance of " + (C && C.name), "expected " + show(v) + " not to be an instance of " + (C && C.name));
};
function compare(names, test, word) {
  names.forEach(function (name) {
    Assertion.prototype[name] = function (n) {
      var v = this._v;
      return this._assert(test(v, n), "expected " + show(v) + " to be " + word + " " + n, "expected " + show(v) + " not to be " + word + " " + n);
    };
  });
}
compare(["above", "gt", "greaterThan"], function (v, n) { return v > n; }, "above");
compare(["below", "lt", "lessThan"], function (v, n) { return v < n; }, "below");
compare(["least", "gte", "greaterThanOrEqual"], function (v, n) { return v >= n; }, "at least");
compare(["most", "lte", "lessThanOrEqual"], function (v, n) { return v <= n; }, "at most");
Assertion.prototype.within = function (lo, hi) {
  var v = this._v;
  return this._assert(v >= lo && v <= hi, "expected " + show(v) + " to be within " + lo + ".." + hi, "expected " + show(v) + " not to be within " + lo + ".." + hi);
};
// a / an work as a call (.an("array")) and as a chain (.an.instanceOf(X)).
["a", "an"].forEach(function (name) {
  Object.defineProperty(Assertion.prototype, name, {
    get: function () {
      var self = this;
      var fn = function (type) {
        var v = self._v, t = typeName(v);
        return self._assert(t === String(type).toLowerCase(), "expected " + show(v) + " to be a " + type + " but got " + t, "expected " + show(v) + " not to be a " + type);
      };
      Object.setPrototypeOf(fn, self);
      return fn;
    },
  });
});
Assertion.prototype.match = Assertion.prototype.matches = function (re) {
  var v = this._v;
  return this._assert(re.test(String(v)), "expected " + show(v) + " to match " + re, "expected " + show(v) + " not to match " + re);
};
Assertion.prototype.oneOf = function (list) {
  var v = this._v;
  return this._assert(list.some(function (e) { return deepEqual(e, v); }), "expected " + show(v) + " to be one of " + show(list), "expected " + show(v) + " not to be one of " + show(list));
};
Assertion.prototype.keys = Assertion.prototype.key = function () {
  var want = Array.isArray(arguments[0]) ? arguments[0] : Array.prototype.slice.call(arguments);
  var v = this._v, have = v && typeof v === "object" ? Object.keys(v) : [];
  var ok = want.every(function (k) { return have.indexOf(k) >= 0; });
  return this._assert(ok, "expected " + show(v) + " to have keys " + show(want), "expected " + show(v) + " not to have keys " + show(want));
};
Assertion.prototype.status = function (code) {
  var r = this._v;
  if (typeof code === "string") {
    return this._assert(r.status === code, "expected response status " + show(r.status) + " to be " + show(code), "expected response status not to be " + show(code));
  }
  return this._assert(r.code === code, "expected response to have status code " + code + " but got " + r.code, "expected response not to have status code " + code);
};
Assertion.prototype.header = function (name, value) {
  var r = this._v, got = r.headers.get(name);
  var ok = got !== null && (arguments.length < 2 || got === value);
  var what = "header " + show(name) + (arguments.length > 1 ? " with value " + show(value) : "");
  return this._assert(ok, "expected response to have " + what + (got !== null ? " (got " + show(got) + ")" : ""), "expected response not to have " + what);
};
Assertion.prototype.body = function (text) {
  var r = this._v;
  var ok = arguments.length === 0 ? r.text() !== "" : r.text() === text;
  return this._assert(ok, "expected response body " + (arguments.length ? "to be " + show(text) : "not to be empty"), "expected response body not to be " + show(text));
};
Assertion.prototype.jsonBody = function (path, value) {
  var r = this._v, body;
  try { body = r.json(); } catch (e) { return this._assert(false, "expected response to have a JSON body", "expected response not to have a JSON body"); }
  if (arguments.length === 0) return this._assert(true, "", "expected response not to have a JSON body");
  var a = new Assertion(body);
  a._nested = true;
  a._not = this._not;
  return arguments.length > 1 ? a.property(path, value) : a.property(path);
};
Assertion.prototype.jsonSchema = function () {
  throw new Error("jsonSchema assertions are not supported by monica");
};

module.exports = {
  Assertion: Assertion,
  expect: function (v) { return new Assertion(v); },
  deepEqual: deepEqual,
  show: show,
};
