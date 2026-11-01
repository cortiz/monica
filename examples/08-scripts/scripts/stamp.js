// A pre-request script file: shared by several requests.
req.headers.set("X-Stamp", "stamp-" + Date.now());
vars.set("lastStamp", req.headers.get("X-Stamp"));
