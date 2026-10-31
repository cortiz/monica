type Shape = { x: number };
// line 2

export function boom(s?: Shape): never {
  throw new Error("ts boom " + (s?.x ?? "none"));
}
