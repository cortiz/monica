// Postman compatibility for scripts imported by `monica import postman`.
// Each imported script starts with:
//
//   const pm = require("monica-postman")();
//
// pm.* reads monica's req, res and vars when it is called. Supported: pm.test,
// pm.expect (a subset of Chai's BDD style), pm.response, pm.request,
// pm.environment / collectionVariables / globals / variables / iterationData
// (all backed by monica's vars), pm.info. Anything else throws, naming the
// missing feature; the import report lists the scripts that use one.
"use strict";

var chai = require("./monica-chai");
var Assertion = chai.Assertion;

function scope(name) {
  return {
    get: function (k) { return vars.get(k); },
    set: function (k, v) { vars.set(k, v === undefined || v === null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v)); },
    has: function (k) { return vars.has(k); },
    unset: function (k) { vars.set(k, ""); },
    clear: function () { throw new Error("pm." + name + ".clear() is not supported by monica"); },
    replaceIn: function (s) {
      return String(s).replace(/{{\s*([^{}]+?)\s*}}/g, function (m, k) { return vars.has(k) ? vars.get(k) : m; });
    },
    toObject: function () { throw new Error("pm." + name + ".toObject() is not supported by monica"); },
  };
}

function headerList(h, writable) {
  return {
    get: function (n) { return h.get(n); },
    has: function (n) { return h.has(n); },
    toObject: function () { return h.all(); },
    add: function (x) { if (!writable) throw new Error("request headers are read-only after sending"); h.add(x.key, x.value); },
    upsert: function (x) { if (!writable) throw new Error("request headers are read-only after sending"); h.set(x.key, x.value); },
    remove: function (n) { if (!writable) throw new Error("request headers are read-only after sending"); h.delete(n); },
  };
}

function makeResponse() {
  if (typeof res === "undefined") return undefined;
  var r = {
    __monicaResponse: true,
    code: res.status,
    status: res.statusText,
    responseTime: res.time,
    headers: headerList(res.headers, false),
    json: function () { return res.json(); },
    text: function () { return res.body; },
    size: function () { return { body: res.body.length }; },
  };
  Object.defineProperty(r, "to", { get: function () { return new Assertion(r); } });
  Object.defineProperty(r, "responseSize", { get: function () { return res.body.length; } });
  return r;
}

function unsupported(name) {
  return function () { throw new Error(name + " is not supported by monica"); };
}

module.exports = function () {
  var pre = typeof res === "undefined";
  var pm = {
    test: function (name, fn) { test(name, function () { fn(function () {}); }); },
    expect: function (v) { return new Assertion(v); },
    environment: scope("environment"),
    collectionVariables: scope("collectionVariables"),
    globals: scope("globals"),
    variables: scope("variables"),
    iterationData: scope("iterationData"),
    info: { iteration: monica.iteration, requestName: "", eventName: pre ? "prerequest" : "test" },
    request: {
      method: req.method,
      url: { toString: function () { return req.url; } },
      headers: headerList(req.headers, pre),
    },
    response: makeResponse(),
    sendRequest: unsupported("pm.sendRequest"),
    execution: { setNextRequest: unsupported("pm.execution.setNextRequest"), skipRequest: function () { req.skip("pm.execution.skipRequest()"); } },
    visualizer: { set: unsupported("pm.visualizer") },
  };
  pm.environment.name = monica.env;
  return pm;
};
