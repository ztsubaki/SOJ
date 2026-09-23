import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { mkdirSync, rmSync } from "node:fs";

const require = createRequire(new URL("../package.json", import.meta.url));
const root = fileURLToPath(new URL("../../../proto/", import.meta.url));
const out = fileURLToPath(new URL("../src/generated/", import.meta.url));

// The generated bindings include message codecs and grpc-js clients. Runtime
// code therefore never needs to locate or parse .proto source files.
rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
execFileSync(process.execPath, [
  require.resolve("grpc-tools/bin/protoc.js"),
  `--plugin=protoc-gen-ts_proto=${require.resolve("ts-proto/protoc-gen-ts_proto")}`,
  `--ts_proto_out=env=both,outputServices=grpc-js,esModuleInterop=true,forceLong=number,useDate=false,useOptionals=all,stringEnums=true:${out}`,
  "-I", ".",
  ...["auth", "admin", "contests", "submissions", "stats"].map(name => `soj/v1/${name}.proto`),
], { cwd: root, stdio: "inherit" });
