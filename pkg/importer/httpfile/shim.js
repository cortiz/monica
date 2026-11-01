// JetBrains HTTP Client compatibility for scripts imported by
// `monica import http`. Each imported handler starts with:
//
//   const { client, request, response, jsonPath } = require("monica-http")();
//
// which gives the script JetBrains-style objects backed by monica's req, res
// and vars. Supported: client.test / assert / log, client.global.set / get /
// clear / clearAll / isEmpty (monica vars), response.status / body (parsed
// when the content type is JSON) / headers.valueOf / valuesOf / contentType,
// request.method / url / body / headers, request.variables.set / get,
// request.environment.get, jsonPath(value, path). Anything else throws,
// naming the missing feature; the import report lists scripts that use one.
//
// dynamic(expr) computes the .http dynamic variables monica has no built-in
// for, and capture() the VS Code request variables.
"use strict";

var jp = require("monica/jsonpath");
var random = require("monica/random");

// Names set with client.global.set during the run (module state lasts the run).
var globals = {};

function str(v) {
  if (v === undefined || v === null) return "";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

function unsupported(name) {
  return function () { throw new Error(name + " is not supported by monica"); };
}

function interpolate(s) {
  if (s === undefined || s === null) return s;
  return String(s).replace(/{{\s*([^{}]+?)\s*}}/g, function (m, k) {
    if (k === "$uuid" || k === "$random.uuid") return random.uuid();
    if (k === "$timestamp") return String(Math.floor(Date.now() / 1000));
    if (k === "$randomInt") return String(random.int(0, 999));
    return vars.has(k) ? vars.get(k) : m;
  });
}

function headerValues(h, name) {
  var all = h.all(), key = String(name).toLowerCase(), out = [];
  Object.keys(all).forEach(function (k) {
    if (k.toLowerCase() === key) out = out.concat(all[k]);
  });
  return out;
}

function contentTypeOf(value) {
  var ct = { mimeType: "", charset: "" };
  if (!value) return ct;
  var parts = String(value).split(";");
  ct.mimeType = parts[0].trim().toLowerCase();
  parts.slice(1).forEach(function (p) {
    var kv = p.split("=");
    if (kv[0].trim().toLowerCase() === "charset") ct.charset = (kv[1] || "").trim().replace(/^"|"$/g, "");
  });
  return ct;
}

var client = {
  test: function (name, fn) { test(name, fn); },
  assert: function (condition, message) {
    if (!condition) throw new Error(message || "Assertion failed");
  },
  log: function () { console.log.apply(console, arguments); },
  global: {
    set: function (k, v) { vars.set(k, str(v)); globals[k] = true; },
    get: function (k) { return globals[k] || vars.has(k) ? vars.get(k) : null; },
    clear: function (k) { vars.set(k, ""); delete globals[k]; },
    clearAll: function () {
      Object.keys(globals).forEach(function (k) { vars.set(k, ""); });
      globals = {};
    },
    isEmpty: function () { return Object.keys(globals).length === 0; },
  },
  exit: unsupported("client.exit"),
};

function header(name, value) {
  return {
    name: name,
    value: value,
    getRawValue: function () { return value; },
    tryGetSubstitutedValue: function () { return interpolate(value); },
  };
}

function makeRequest() {
  return {
    method: req.method,
    url: {
      getRaw: function () { return req.url; },
      tryGetSubstituted: function () { return interpolate(req.url); },
    },
    body: {
      getRaw: function () { return req.body; },
      tryGetSubstituted: function () { return interpolate(req.body); },
    },
    headers: {
      all: function () {
        var all = req.headers.all(), out = [];
        Object.keys(all).forEach(function (k) {
          all[k].forEach(function (v) { out.push(header(k, v)); });
        });
        return out;
      },
      findByName: function (n) {
        var v = headerValues(req.headers, n);
        return v.length ? header(n, v[0]) : null;
      },
    },
    variables: {
      set: function (k, v) { vars.set(k, str(v)); },
      get: function (k) { return vars.has(k) ? vars.get(k) : null; },
    },
    environment: {
      get: function (k) { return vars.has(k) ? vars.get(k) : null; },
    },
    iteration: function () { return monica.iteration; },
  };
}

function makeResponse() {
  if (typeof res === "undefined") return undefined;
  var ct = contentTypeOf(res.headers.get("Content-Type"));
  var body = res.body;
  if (/(^|[/+])json$/.test(ct.mimeType) && body !== "") {
    try { body = JSON.parse(body); } catch (e) { /* keep the text */ }
  }
  return {
    status: res.status,
    body: body,
    contentType: ct,
    headers: {
      valueOf: function (n) {
        var v = headerValues(res.headers, n);
        return v.length ? v[0] : null;
      },
      valuesOf: function (n) { return headerValues(res.headers, n); },
    },
  };
}

function jsonPath(value, path) { return jp.value(value, path); }

// --- dynamic variables ---------------------------------------------------

function args(s) {
  var out = [], re = /"([^"]*)"|'([^']*)'|(\S+)/g, m;
  while ((m = re.exec(s)) !== null) out.push(m[1] !== undefined ? m[1] : m[2] !== undefined ? m[2] : m[3]);
  return out;
}

var unitMs = { ms: 1, s: 1000, m: 60000, h: 3600000, d: 86400000, w: 604800000 };

// offset applies "<n> <unit>" (y, M, w, d, h, m, s, ms) to a Date.
function offset(d, a) {
  if (a.length < 2) return d;
  var n = Number(a[0]), u = a[1];
  if (isNaN(n)) throw new Error("dynamic variable offset " + a.join(" ") + " is not a number");
  if (u === "y") d.setFullYear(d.getFullYear() + n);
  else if (u === "M") d.setMonth(d.getMonth() + n);
  else if (unitMs[u]) d.setTime(d.getTime() + n * unitMs[u]);
  else throw new Error("dynamic variable offset unit " + u + " is not supported");
  return d;
}

function pad(n, w) { n = String(n); while (n.length < w) n = "0" + n; return n; }

// format writes d with Day.js-style tokens (YYYY, MM, DD, HH, mm, ss, SSS, ...).
function format(d, f, utc) {
  var g = function (name) { return d["get" + (utc ? "UTC" : "") + name](); };
  var months = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
  var days = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
  var tz = function (sep) {
    if (utc) return "+00" + sep + "00";
    var o = -d.getTimezoneOffset(), s = o < 0 ? "-" : "+";
    o = Math.abs(o);
    return s + pad(Math.floor(o / 60), 2) + sep + pad(o % 60, 2);
  };
  var t = {
    YYYY: function () { return pad(g("FullYear"), 4); },
    YY: function () { return pad(g("FullYear") % 100, 2); },
    MMMM: function () { return months[g("Month")]; },
    MMM: function () { return months[g("Month")].slice(0, 3); },
    MM: function () { return pad(g("Month") + 1, 2); },
    M: function () { return String(g("Month") + 1); },
    DD: function () { return pad(g("Date"), 2); },
    D: function () { return String(g("Date")); },
    dddd: function () { return days[g("Day")]; },
    ddd: function () { return days[g("Day")].slice(0, 3); },
    d: function () { return String(g("Day")); },
    HH: function () { return pad(g("Hours"), 2); },
    H: function () { return String(g("Hours")); },
    hh: function () { return pad(g("Hours") % 12 || 12, 2); },
    h: function () { return String(g("Hours") % 12 || 12); },
    mm: function () { return pad(g("Minutes"), 2); },
    m: function () { return String(g("Minutes")); },
    ss: function () { return pad(g("Seconds"), 2); },
    s: function () { return String(g("Seconds")); },
    SSS: function () { return pad(g("Milliseconds"), 3); },
    A: function () { return g("Hours") < 12 ? "AM" : "PM"; },
    a: function () { return g("Hours") < 12 ? "am" : "pm"; },
    ZZ: function () { return tz(""); },
    Z: function () { return tz(":"); },
  };
  return f.replace(/\[([^\]]*)]|YYYY|YY|MMMM|MMM|MM|M|DD|D|dddd|ddd|d|HH|H|hh|h|mm|m|ss|s|SSS|A|a|ZZ|Z/g, function (tok, lit) {
    return lit !== undefined ? lit : t[tok]();
  });
}

function datetime(a, utc) {
  var kind = a[0] || "iso8601";
  var d = offset(new Date(), a.slice(1));
  if (kind === "iso8601") return utc ? d.toISOString() : format(d, "YYYY-MM-DDTHH:mm:ss.SSSZ", false);
  if (kind === "rfc1123") return d.toUTCString();
  return format(d, kind, utc);
}

function chars(n, alphabet) { return random.string(Number(n) || 10, alphabet); }

function call(expr, name) {
  var m = new RegExp("^" + name.replace(/[$.]/g, "\\$&") + "\\(([^)]*)\\)$").exec(expr);
  if (!m) return [];
  return m[1].split(",").map(function (x) { return x.trim(); }).filter(function (x) { return x !== ""; });
}

// dynamic computes a .http dynamic variable ("$datetime iso8601 1 d",
// "$random.integer(1, 10)", ...).
function dynamic(expr) {
  var name = /^\$[A-Za-z.]+/.exec(expr)[0], rest = expr.slice(name.length).trim(), a = args(rest), p;
  switch (name) {
    case "$isoTimestamp": return new Date().toISOString();
    case "$timestamp": return String(Math.floor(offset(new Date(), a).getTime() / 1000));
    case "$datetime": return datetime(a, true);
    case "$localDatetime": return datetime(a, false);
    case "$randomInt": // VS Code: [min, max)
      return String(random.int(Number(a[0] || 0), Number(a[1] || 1000) - 1));
    case "$random.integer":
      p = call(expr, name);
      return String(random.int(Number(p[0] || 0), Number(p[1] || 1000) - 1));
    case "$random.float":
      p = call(expr, name);
      var lo = Number(p[0] || 0), hi = Number(p[1] || 1000);
      return String(lo + (random.int(0, 1e9) / 1e9) * (hi - lo));
    case "$random.alphabetic": return chars(call(expr, name)[0], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz");
    case "$random.alphanumeric": return chars(call(expr, name)[0], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_");
    case "$random.hexadecimal": return chars(call(expr, name)[0], "0123456789abcdef");
    case "$random.email": return random.string(10, "abcdefghijklmnopqrstuvwxyz0123456789") + "@example.com";
  }
  throw new Error("dynamic variable {{" + expr + "}} is not supported by monica");
}

// capture reads a VS Code request variable ({{name.response.body.$.x}})
// from the request that just ran.
function capture(source, part, sel) {
  var isRes = source === "response";
  var h = isRes ? res.headers : req.headers;
  if (part === "headers") return headerValues(h, sel).join(", ");
  var body = isRes ? res.body : req.body;
  if (sel === "*") return str(body);
  if (sel.charAt(0) === "/") throw new Error("XPath request variables (" + sel + ") are not supported by monica");
  return str(jp.value(body, sel.charAt(0) === "$" ? sel : "$." + sel));
}

module.exports = function () {
  return { client: client, request: makeRequest(), response: makeResponse(), jsonPath: jsonPath };
};
module.exports.dynamic = dynamic;
module.exports.capture = capture;
