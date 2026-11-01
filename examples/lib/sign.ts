// An ES module in TypeScript: types are stripped when it is loaded.
import { hmac } from "monica/crypto";

export function signature(secret: string, body: string): string {
  return hmac("sha256", secret, body, "base64");
}
