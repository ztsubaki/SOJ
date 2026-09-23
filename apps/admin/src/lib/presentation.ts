export const verdictColor: Record<
  string,
  "success" | "error" | "warning" | "info" | "primary"
> = {
  pending: "info",
  judging: "primary",
  accepted: "success",
  wrong_answer: "error",
  compile_error: "info",
  runtime_error: "error",
  time_limit: "warning",
  memory_limit: "warning",
  cancelled: "warning",
  system_error: "error",
};
export const difficultyColor: Record<
  string,
  "success" | "warning" | "error" | "primary"
> = { Easy: "success", Medium: "warning", Hard: "error" };
export const statusColor: Record<
  string,
  "info" | "warning" | "error" | "success"
> = {
  running: "info",
  upcoming: "warning",
  ended: "error",
  active: "success",
  disabled: "error",
};
export const roleColor: Record<string, "primary" | "light"> = {
  admin: "primary",
  user: "light",
};
