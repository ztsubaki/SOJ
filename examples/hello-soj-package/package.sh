#!/usr/bin/env sh
set -eu

problem_id="${1:-4}"
case "$problem_id" in
  *[!0-9]*|'')
    echo "problem ID must be a positive integer" >&2
    exit 2
    ;;
esac
if [ "$problem_id" -eq 0 ]; then
  echo "problem ID must be a positive integer" >&2
  exit 2
fi

package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
output="$package_dir/problem-${problem_id}-example.tar.zst"
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT HUP INT TERM

printf '{\n  "schemaVersion": 1,\n  "problemId": %s\n}\n' "$problem_id" >"$staging/manifest.json"
cp "$package_dir/judge.sbatch" "$staging/judge.sbatch"
cp -R "$package_dir/tests" "$staging/tests"
tar --zstd --format=posix -C "$staging" -cf "$output" manifest.json judge.sbatch tests

echo "created $output"
