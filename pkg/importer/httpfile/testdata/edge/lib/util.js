export function check(response) {
  if (response.status >= 500) throw new Error("server error");
}
