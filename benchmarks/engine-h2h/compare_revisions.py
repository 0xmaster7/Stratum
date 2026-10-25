#!/usr/bin/env python3
"""Alternating old/new GoLSM trials using the unchanged upstream fast workload.
This measures optimization effects; it does not rerun Kiban or LMDB.
"""

import argparse
import datetime
import hashlib
import json
import os
import pathlib
import platform
import statistics
import subprocess

import run as protocol


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--before-binary", required=True, type=pathlib.Path)
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument(
        "--sizes",
        nargs="+",
        type=int,
        choices=[10000, 1000000],
        default=[10000, 1000000],
    )
    parser.add_argument(
        "--baseline-variant", choices=["baseline", "optimized"], default="optimized"
    )
    parser.add_argument(
        "--baseline-metadata",
        type=pathlib.Path,
        default=protocol.ROOT / "results/2026-10-24/metadata.json",
    )
    args = parser.parse_args()
    assert args.runs > 0
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    os.sched_setaffinity(0, {0})
    args.engine_cpus = "1"
    args.keep_databases = False
    baseline = json.loads(args.baseline_metadata.read_text())
    verified = json.loads(args.baseline_metadata.with_name("complete.json").read_text())
    if "binary_sha256" in baseline:
        assert verified["all_value_checks_pass"] and verified["all_reopen_checks_pass"]
        before_hash = baseline["binary_sha256"][args.baseline_variant]
        before_revision = baseline["engine_revisions"][args.baseline_variant]
    else:
        assert verified["zero_value_errors"] and verified["all_reopen_verified"]
        before_hash = baseline["engine_sha256"]
        before_revision = baseline["engine_versions"]["golsm"]
    binaries = {
        "baseline": args.before_binary.resolve(),
        "optimized": protocol.ROOT / "target/release/golsm-engine-h2h",
    }
    hashes = {name: sha(path) for name, path in binaries.items()}
    assert hashes["baseline"] == before_hash, (
        "baseline binary must match its recorded completed campaign"
    )
    revisions = {
        "baseline": before_revision,
        "optimized": (protocol.ROOT / "build/golsm-revision.txt").read_text().strip(),
    }
    assert len(args.sizes) == len(set(args.sizes))
    metadata = {
        "started_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "mode": "fast",
        "runs_per_size_and_revision": args.runs,
        "sizes": args.sizes,
        "baseline_metadata_sha256": sha(args.baseline_metadata),
        "baseline_variant": args.baseline_variant,
        "engine_cpus": "1",
        "driver_cpu": 0,
        "engine_revisions": revisions,
        "binary_sha256": hashes,
        "harness_revision": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=protocol.ROOT, text=True
        ).strip(),
        "kernel": platform.uname()._asdict(),
        "scope": "GoLSM before/after only; original W1-W5 fast workload; no new Kiban/LMDB measurements; generation/oracle/readbacks excluded from call latency but included in verified throughput",
    }
    protocol.dump(out / "metadata.json", metadata)
    for variant in binaries:
        (out / variant).mkdir()
    all_trials = []
    for n in args.sizes:
        ops = 10000 if n == 10000 else 100000
        for trial in range(1, args.runs + 1):
            # Alternate which revision goes first, retaining every result.
            order = (
                ["baseline", "optimized"] if trial % 2 else ["optimized", "baseline"]
            )
            for variant in order:
                binary = binaries[variant]
                assert sha(binary) == hashes[variant]
                summary = protocol.run_trial(
                    args, out / variant, binary, "golsm", "fast", n, ops, trial
                )
                assert (
                    summary["resources"]["engine_settings"]["engine_commit"]
                    == revisions[variant]
                )
                for state in ["initial_engine_state", "final_engine_state"]:
                    metrics = summary["resources"][state]["metrics"]
                    assert metrics["BackgroundError"] == metrics["WriteError"] == ""
                all_trials.append({"variant": variant, "summary": summary})
                protocol.dump(out / "progress.json", {"completed": len(all_trials)})
    expected = len(args.sizes) * 2 * args.runs
    assert len(all_trials) == expected
    aggregate = []
    for n in args.sizes:
        for workload in protocol.STAGES:
            row = {"base_records": n, "workload": workload, "variants": {}}
            for variant in binaries:
                trials = [
                    t["summary"]
                    for t in all_trials
                    if t["variant"] == variant and t["summary"]["base_records"] == n
                ]
                stages = [
                    next(s for s in t["stages"] if s["workload"] == workload)
                    for t in trials
                ]
                row["variants"][variant] = {
                    field: statistics.median(s["latency"][field] for s in stages)
                    for field in ["p50_ns", "p99_ns", "p999_ns", "max_ns"]
                }
                row["variants"][variant].update(
                    {
                        field: statistics.median(s[field] for s in stages)
                        for field in ["verified_ops_s", "call_ops_s"]
                    }
                )
                row["variants"][variant]["all_load_gates_pass"] = all(
                    t["load"]["gate_pass"] for t in trials
                )
                row["variants"][variant]["p50_range_ns"] = [
                    min(s["latency"]["p50_ns"] for s in stages),
                    max(s["latency"]["p50_ns"] for s in stages),
                ]
            aggregate.append(row)
    audit = {
        "completed": expected,
        "all_value_checks_pass": True,
        "all_reopen_checks_pass": True,
        "all_load_gates_pass": all(
            t["summary"]["load"]["gate_pass"] for t in all_trials
        ),
        "timed_calls": sum(
            s["latency"]["count"] for t in all_trials for s in t["summary"]["stages"]
        ),
        "finished_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    }
    protocol.dump(
        out / "comparison.json",
        {"metadata": metadata, "audit": audit, "rows": aggregate},
    )
    protocol.dump(out / "complete.json", audit)
    print("REVISION COMPARISON COMPLETE", expected, flush=True)


if __name__ == "__main__":
    main()
