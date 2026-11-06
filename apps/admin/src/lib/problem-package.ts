const packageFormatHint =
  "测评包必须是 .tar.zst；解包根目录需包含 manifest.json，Slurm 包需包含 judge.sbatch，Docker 包需包含 judge.sh。";

export const problemPackageAccept = ".tar.zst,application/zstd";

export function problemPackageHint(problemID: string | number) {
  return `${packageFormatHint} manifest.json 必须包含 schemaVersion: 1 和 problemId: ${problemID}；Slurm 包不含 docker 字段。Docker 包还需包含 docker 配置，例如 {"image":"judge-image@sha256:${"a".repeat(64)}","cpus":1,"memoryMb":128,"timeLimitSeconds":10}，将示例摘要替换为部署时已预加载镜像的真实摘要；也支持 sha256: 加 64 位小写十六进制镜像 ID。cpus 为 1–256 的整数，memoryMb 为 16–1048576 的整数（MiB），timeLimitSeconds 为 1–86400 的整数，配置不能缺项或添加未知字段。Docker 使用 /bin/sh /judge/package/judge.sh，镜像必须含 /bin/sh，不会自动拉取。`;
}

export async function problemPackageUploadError(
  response: Response,
  savedMessage: string,
  problemID: string | number,
) {
  const body = (await response.json().catch(() => null)) as {
    error?: unknown;
  } | null;
  const upstreamError =
    typeof body?.error === "string" ? body.error : "";

  if (response.status === 400 && upstreamError === "invalid problem package") {
    return new Error(`${savedMessage}，但测评包格式不正确。${problemPackageHint(problemID)}`);
  }
  return new Error(
    upstreamError
      ? `${savedMessage}，但测评包上传失败：${upstreamError}`
      : `${savedMessage}，但测评包上传失败。`,
  );
}
