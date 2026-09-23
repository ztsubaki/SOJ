export function contestSummary(value: string) {
  const firstLine = value.trimStart().split(/\r?\n/, 1)[0] ?? "";
  return firstLine.replace(/^\s{0,3}#{1,6}[ \t]+/, "").trim();
}
