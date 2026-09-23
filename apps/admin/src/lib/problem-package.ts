const packageFormatHint =
  "测评包必须是 .tar.zst；解包根目录需包含 manifest.json 和 judge.sbatch。";

export const problemPackageAccept = ".tar.zst,application/zstd";

export function problemPackageHint(problemID: string | number) {
  return `${packageFormatHint} manifest.json 只能包含 schemaVersion: 1 和 problemId: ${problemID}。`;
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
