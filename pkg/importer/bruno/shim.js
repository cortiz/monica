// Bruno compatibility for scripts imported by `monica import bruno`. Each
// imported script starts with:
//
//   const { bru, req, res, expect } = require("monica-bruno")();
//
// which shadows monica's req/res/expect with Bruno-style objects for the
// script's own scope. Supported: bru.getVar/setVar/getEnvVar/setEnvVar
// (monica vars), bru.getEnvName, bru.interpolate, req.get/set Url, Method,
// Header(s), Body; res.status/statusText/headers/body/responseTime,
// res.getStatus/getHeader/getHeaders/getBody/getResponseTime, res("a.b")
// queries; expect() is Chai-style. Anything else throws, naming the
// missing feature; the import report lists scripts that use one.
"use strict";

var chai = require("./monica-chai");

function interpolate(s) {
  return String(s).replace(/{{\s*([^{}]+?)\s*}}/g, function (m, k) {
    if (k === "$uuid" || k === "$guid" || k === "$randomUUID") return require("monica/random").uuid();
    if (k === "$timestamp") return String(Math.floor(Date.now() / 1000));
    if (k === "$randomInt") return String(require("monica/random").int(0, 999));
    return vars.has(k) ? vars.get(k) : m;
  });
}

function str(v) {
  if (v === undefined || v === null) return "";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

function unsupported(name) {
  return function () { throw new Error(name + " is not supported by monica"); };
}

function makeBru() {
  return {
    getVar: function (k) { return vars.get(k); },
    setVar: function (k, v) { vars.set(k, str(v)); },
    hasVar: function (k) { return vars.has(k); },
    deleteVar: function (k) { vars.set(k, ""); },
    getEnvVar: function (k) { return vars.get(k); },
    setEnvVar: function (k, v) { vars.set(k, str(v)); },
    getCollectionVar: function (k) { return vars.get(k); },
    getFolderVar: function (k) { return vars.get(k); },
    getRequestVar: function (k) { return vars.get(k); },
    getEnvName: function () { return monica.env; },
    interpolate: interpolate,
    getProcessEnv: unsupported("bru.getProcessEnv (use ${NAME} values in monica.yml)"),
    setNextRequest: unsupported("bru.setNextRequest"),
    runRequest: unsupported("bru.runRequest"),
    sendRequest: unsupported("bru.sendRequest"),
    sleep: unsupported("bru.sleep"),
    runner: { skipRequest: function () { req.skip("bru.runner.skipRequest()"); }, stopExecution: unsupported("bru.runner.stopExecution") },
  };
}

function makeReq() {
  var r = req;
  return {
    getUrl: function () { return r.url; },
    setUrl: function (u) { r.url = u; },
    getMethod: function () { return r.method; },
    setMethod: function (m) { r.method = m; },
    getHeader: function (n) { return r.headers.get(n); },
    setHeader: function (n, v) { r.headers.set(n, str(v)); },
    deleteHeader: function (n) { r.headers.delete(n); },
    getHeaders: function () {
      var out = {}, all = r.headers.all();
      Object.keys(all).forEach(function (k) { out[k.toLowerCase()] = all[k].join(", "); });
      return out;
    },
    setHeaders: function (h) { Object.keys(h).forEach(function (k) { r.headers.set(k, str(h[k])); }); },
    getBody: function () {
      try { return JSON.parse(r.body); } catch (e) { return r.body; }
    },
    setBody: function (b) { r.body = typeof b === "string" ? b : JSON.stringify(b); },
    get url() { return r.url; },
    get method() { return r.method; },
  };
}

function makeRes() {
  if (typeof res === "undefined") return undefined;
  var R = res, body;
  try { body = R.json(); } catch (e) { body = R.body; }
  var headers = {}, all = R.headers.all();
  Object.keys(all).forEach(function (k) { headers[k.toLowerCase()] = all[k].join(", "); });
  // res("a.b[0].c") queries the body, as in Bruno.
  var b = function (path) {
    return String(path).replace(/\[(\d+)\]/g, ".$1").split(".").filter(Boolean).reduce(function (o, k) {
      return o === undefined || o === null ? undefined : o[k];
    }, body);
  };
  b.status = R.status;
  b.statusText = R.statusText;
  b.headers = headers;
  b.body = body;
  b.responseTime = R.time;
  b.getStatus = function () { return R.status; };
  b.getStatusText = function () { return R.statusText; };
  b.getHeader = function (n) { return headers[String(n).toLowerCase()]; };
  b.getHeaders = function () { return headers; };
  b.getBody = function () { return body; };
  b.getResponseTime = function () { return R.time; };
  b.setBody = unsupported("res.setBody");
  return b;
}

module.exports = function () {
  return { bru: makeBru(), req: makeReq(), res: makeRes(), expect: chai.expect };
};
