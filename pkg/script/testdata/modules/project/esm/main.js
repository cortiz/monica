// An ES module: imports TypeScript without its extension (and with it).
import greet, { add } from "./util";
import * as again from "./util.ts";

export const result = `${greet("ada")} ${add({ a: 1, b: 2 })} ${again.add({ a: 2, b: 2 })}`;
