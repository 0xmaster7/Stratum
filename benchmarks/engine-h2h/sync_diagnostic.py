#!/usr/bin/env python3
"""Excluded kernel-syscall diagnostic, run only after the campaign completes.
Go bypasses libc for fsync, so LD_PRELOAD alone cannot audit all three engines.
"""

import argparse, json, os, pathlib, re, shutil, subprocess

ROOT = pathlib.Path(__file__).resolve().parent


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=pathlib.Path)
    args = parser.parse_args()
    root = args.results.resolve()
    assert (root / "complete.json").exists(), "finish the uninstrumented campaign first"
    if not shutil.which("strace"):
        raise SystemExit("strace is required for this optional diagnostic")
    out = root / "sync-diagnostic"
    out.mkdir(exist_ok=False)
    os.sched_setaffinity(0, {0})
    binary = ROOT / "target/release/golsm-engine-h2h"
    results = []
    for mode in ["durable", "fast"]:
        for engine in ["kiban", "golsm", "lmdb"]:
            prefix = f"{mode}-{engine}"
            config = json.loads(
                (root / f"n10000-{mode}-r1-{engine}.config.json").read_text()
            )
            config.update(
                db=str(out / f"{prefix}.db"),
                operations=1000,
                write_operations=1000,
                output=str(out / f"{prefix}.jsonl"),
                verify_only=False,
            )
            path = out / f"{prefix}.config.json"
            path.write_text(json.dumps(config, indent=2) + "\n")
            trace = out / f"{prefix}.syscalls.txt"
            cmd = [
                "strace",
                "-f",
                "-qq",
                "-c",
                "-e",
                "trace=fsync,fdatasync,pwrite64",
                "-o",
                str(trace),
                "taskset",
                "-c",
                "1",
                str(binary),
                str(path),
            ]
            with (out / f"{prefix}.log").open("w") as f:
                subprocess.run(cmd, stdout=f, stderr=subprocess.STDOUT, check=True)
            counts = {}
            for line in trace.read_text().splitlines():
                m = re.match(
                    r"^\s*[\d.]+\s+[\d.]+\s+\d+\s+(\d+)(?:\s+(\d+))?\s+(fsync|fdatasync|pwrite64)\s*$",
                    line,
                )
                if m:
                    counts[m[3]] = {"calls": int(m[1]), "errors": int(m[2] or 0)}
            count = lambda name: counts.get(name, {}).get("calls", 0)
            if mode == "durable":
                assert count("fsync" if engine == "golsm" else "fdatasync") >= 2100, (
                    engine,
                    counts,
                )
            else:
                assert count("fsync") + count("fdatasync") < 2100, (engine, counts)
            rows = [
                json.loads(line)
                for line in (out / f"{prefix}.jsonl").read_text().splitlines()
            ]
            assert (
                rows[-1]["type"] == "complete" and rows[-1]["verified_records"] == 11000
            )
            result = {
                "mode": mode,
                "engine": engine,
                "excluded_from_latency_comparison": True,
                "timed_writes": 2100,
                "scope": "whole process including warmup, bulk preload, final sync/verification, and close",
                "syscalls": counts,
                "command": cmd,
            }
            results.append(result)
            shutil.rmtree(config["db"])
            print(prefix, counts, flush=True)
    (out / "summary.json").write_text(json.dumps(results, indent=2) + "\n")


if __name__ == "__main__":
    main()
