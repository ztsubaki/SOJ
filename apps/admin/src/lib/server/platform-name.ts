import "server-only";
import { connection } from "next/server";

const defaultPlatformName = "SOJ";

export async function getPlatformName() {
  await connection();
  return process.env.PLATFORM_NAME?.trim() || defaultPlatformName;
}
