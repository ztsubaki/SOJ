// datetime-local has no timezone. Convert explicitly at the browser boundary
// so an ISO instant is never rendered as UTC wall-clock time.
export function toLocalDateTime(value: string) {
  const date = new Date(value);
  if (!value || !Number.isFinite(date.getTime())) return "";
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
    .toISOString()
    .slice(0, 16);
}

export function fromLocalDateTime(value: string) {
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toISOString() : "";
}
