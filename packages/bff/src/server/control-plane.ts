import "server-only";
import { connection } from "next/server";
import * as grpc from "@grpc/grpc-js";
import { readFileSync } from "node:fs";
import { AuthServiceClient } from "../generated/soj/v1/auth";
import {
  AdminContestServiceClient,
  AdminProblemServiceClient,
  AdminSubmissionServiceClient,
  AdminSystemServiceClient,
  AdminUserServiceClient,
} from "../generated/soj/v1/admin";
import { ContestServiceClient } from "../generated/soj/v1/contests";
import { SubmissionServiceClient } from "../generated/soj/v1/submissions";
import { StatsServiceClient } from "../generated/soj/v1/stats";
type Services = {
  AuthService: AuthServiceClient;
  ContestService: ContestServiceClient;
  SubmissionService: SubmissionServiceClient;
  StatsService: StatsServiceClient;
  AdminProblemService: AdminProblemServiceClient;
  AdminContestService: AdminContestServiceClient;
  AdminSubmissionService: AdminSubmissionServiceClient;
  AdminUserService: AdminUserServiceClient;
  AdminSystemService: AdminSystemServiceClient;
};
type Method<S extends keyof Services> = Exclude<
  keyof Services[S],
  keyof grpc.Client
> &
  string;
type UnaryMethod<Request, Response> = {
  (
    request: Request,
    metadata: grpc.Metadata,
    options: grpc.CallOptions,
    callback: (error: grpc.ServiceError | null, response: Response) => void,
  ): grpc.ClientUnaryCall;
};
type Call<
  S extends keyof Services,
  M extends Method<S>,
> = Services[S][M] extends UnaryMethod<infer Request, infer Response>
  ? { request: Request; response: Response }
  : never;
export type RPC = <S extends keyof Services, M extends Method<S>>(
  service: S,
  method: M,
  request: Call<S, M>["request"],
  session?: string,
) => Promise<Call<S, M>["response"]>;

type UnaryClient = Record<
  string,
  (
    request: object,
    metadata: grpc.Metadata,
    options: grpc.CallOptions,
    callback: grpc.requestCallback<unknown>,
  ) => grpc.ClientUnaryCall
>;
const serviceConstructors = {
  AuthService: AuthServiceClient,
  ContestService: ContestServiceClient,
  SubmissionService: SubmissionServiceClient,
  StatsService: StatsServiceClient,
  AdminProblemService: AdminProblemServiceClient,
  AdminContestService: AdminContestServiceClient,
  AdminSubmissionService: AdminSubmissionServiceClient,
  AdminUserService: AdminUserServiceClient,
  AdminSystemService: AdminSystemServiceClient,
};

export function configured(name: string) {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required to call the Control Plane`);
  return value;
}
export function controlPlaneHTTPURL() {
  return configured("CONTROL_PLANE_HTTP_URL").replace(/\/$/, "");
}

export function createRPC(
  serviceTokenEnv: "WEB_BFF_SERVICE_TOKEN" | "ADMIN_BFF_SERVICE_TOKEN",
  names: (keyof Services)[],
): RPC {
  let clients: Record<string, UnaryClient> | undefined;
  function getClients() {
    if (clients) return clients;
    const address = configured("GRPC_ADDR");
    const insecure = ["true", "1"].includes(
      process.env.GRPC_INSECURE_DEV ?? "",
    );
    const host = address.startsWith("[")
      ? address.slice(1, address.indexOf("]"))
      : address.split(":")[0];
    if (insecure && !["localhost", "127.0.0.1", "::1"].includes(host))
      throw new Error("GRPC_INSECURE_DEV requires a loopback GRPC_ADDR");
    const caFile = process.env.GRPC_TLS_CA_FILE;
    const credentials = insecure
      ? grpc.credentials.createInsecure()
      : grpc.credentials.createSsl(caFile ? readFileSync(caFile) : undefined);
    const tlsOptions =
      insecure || !process.env.GRPC_TLS_SERVER_NAME
        ? {}
        : { "grpc.ssl_target_name_override": process.env.GRPC_TLS_SERVER_NAME };
    const options = {
      ...tlsOptions,
      "grpc.max_receive_message_length": 64 * 1024 * 1024,
      "grpc.max_send_message_length": 64 * 1024 * 1024,
    };
    clients = Object.fromEntries(
      names.map((name) => [
        name,
        new serviceConstructors[name](address, credentials, options),
      ]),
    ) as unknown as Record<string, UnaryClient>;
    return clients;
  }
  // Static protocol bindings are confined to this boundary; callers receive typed requests and responses.
  return (async (
    service: string,
    method: string,
    request: object,
    session?: string,
  ) => {
    await connection();
    const client = getClients()[service];
    if (!client?.[method])
      throw new Error(`unknown Control Plane RPC: ${service}.${method}`);
    const metadata = new grpc.Metadata();
    metadata.set("authorization", `Bearer ${configured(serviceTokenEnv)}`);
    if (session) metadata.set("x-soj-session", session);
    return new Promise((resolve, reject) => {
      client[method].call(
        client,
        request,
        metadata,
        { deadline: new Date(Date.now() + 8_000) },
        (error, response) => (error ? reject(error) : resolve(response)),
      );
    });
  }) as RPC;
}
