#!/usr/bin/env bash
#SBATCH --job-name=soj-hello-example
#SBATCH --time=00:01:00

# This smoke-test package deliberately does not execute submission/source.
# It is safe to use to validate the complete SOJ upload and judge pipeline.
set -euo pipefail

job_id="$(sed -n 's/.*"jobId"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' ./job.json)"
if [ -z "$job_id" ]; then
  exit 1
fi

if cmp -s ./submission/source ./tests/expected-source.txt; then
  verdict="accepted"
  score="100"
  message="exact sample source matched"
else
  verdict="wrong_answer"
  score="0"
  message="submission source did not match the sample"
fi

result_tmp="./output/result.json.tmp"
printf '{"schemaVersion":1,"jobId":%s,"verdict":"%s","score":"%s","message":"%s"}\n' \
  "$job_id" "$verdict" "$score" "$message" >"$result_tmp"
mv "$result_tmp" ./output/result.json
