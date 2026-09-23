export function contestSummary(value: string) {
  return value.replace(/^\s{0,3}#{1,6}[ \t]+/gm, "").trim();
}
