"""Trusted addition application: submission/source is numeric JSON, never code."""

import json
import sys
from pathlib import Path

ROOT = Path("/judge")


def integer(value, name, minimum, maximum):
    if type(value) is not int or not minimum <= value <= maximum:
        raise ValueError(f"{name} must be an integer in [{minimum}, {maximum}]")
    return value


def run():
    context = json.loads((ROOT / "job.json").read_text(encoding="utf-8"))
    job_id = integer(context["jobId"], "jobId", 1, 9007199254740991)
    verdict, score, message = "runtime_error", "0", "Invalid numeric application input"
    exit_code = 1
    try:
        with (ROOT / "submission/source").open("rb") as source:
            payload = source.read((1 << 20) + 1)
        if len(payload) > 1 << 20:
            raise ValueError("numeric input exceeds 1 MiB")
        request = json.loads(payload)
        if type(request) is not dict or set(request) - {
            "a", "b", "expected", "iterations", "retainOutputs"
        }:
            raise ValueError("unexpected application input fields")
        a = integer(request["a"], "a", -(10**12), 10**12)
        b = integer(request["b"], "b", -(10**12), 10**12)
        expected = integer(request["expected"], "expected", -(2 * 10**12), 2 * 10**12)
        iterations = integer(request.get("iterations", 1), "iterations", 1, 10**12)
        retain = request.get("retainOutputs", False)
        if type(retain) is not bool:
            raise ValueError("retainOutputs must be a boolean")
        # Real application work. A large iteration count exercises CPU/time or
        # cancellation; retaining a 1 MiB materialized output per iteration
        # exercises the actual cgroup memory limit, not a simulated verdict.
        outputs = []
        for _ in range(iterations):
            answer = a + b
            if retain:
                outputs.append(bytearray([answer & 255]) * (1 << 20))
        accepted = answer == expected
        verdict = "accepted" if accepted else "wrong_answer"
        score = "100" if accepted else "0"
        message = f"Computed {a} + {b} = {answer}; expected {expected}"
        exit_code = 0 if accepted else 1
    except (ValueError, TypeError, KeyError, UnicodeError) as error:
        # Never copy arbitrary submission text into the structured ASCII message.
        message = f"Invalid numeric application input ({type(error).__name__})"
    result = {
        "schemaVersion": 1,
        "jobId": job_id,
        "verdict": verdict,
        "score": score,
        "message": message,
    }
    output = ROOT / "output"
    temporary = output / "result.json.tmp"
    temporary.write_text(json.dumps(result, separators=(",", ":")) + "\n", encoding="ascii")
    temporary.replace(output / "result.json")
    (output / "judge.log").write_text(message + "\n", encoding="ascii")
    print(message, flush=True)
    return exit_code


if __name__ == "__main__":
    sys.exit(run())
