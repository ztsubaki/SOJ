import "server-only";
import { createRPC } from "@soj/bff/server/control-plane";

export const rpc = createRPC("WEB_BFF_SERVICE_TOKEN", ["AuthService", "ContestService", "SubmissionService", "StatsService"]);
