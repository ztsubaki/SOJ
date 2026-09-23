import "server-only";
import { createRPC } from "@soj/bff/server/control-plane";

export const rpc = createRPC("ADMIN_BFF_SERVICE_TOKEN", [
  "AuthService",
  "AdminProblemService",
  "AdminContestService",
  "AdminSubmissionService",
  "AdminUserService",
  "AdminSystemService",
]);
