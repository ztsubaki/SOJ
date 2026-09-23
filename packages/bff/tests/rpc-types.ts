import type { RPC } from "../src/server/control-plane";

// These checks fail compilation if RPCs ever fall back to untyped string/object calls.
export async function checkRPCContract(rpc: RPC) {
  // @ts-expect-error Removed service names cannot be called.
  await rpc("ArtifactService", "download", {});
  // @ts-expect-error Unknown method names cannot be called.
  await rpc("AuthService", "bootstrap", {});
  // @ts-expect-error Request fields must match the selected RPC.
  await rpc("AuthService", "login", { userName: "runner" });
  // @ts-expect-error Enum strings must match the protocol.
  await rpc("AdminUserService", "listUsers", { role: "root" });
  const session = await rpc("AuthService", "login", {
    username: "runner",
    password: "password",
  });
  // @ts-expect-error Response fields are checked as well.
  return session.isRoot;
}
