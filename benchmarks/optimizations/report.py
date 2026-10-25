#!/usr/bin/env python3
"""Audit and render a complete alternating GoLSM revision comparison."""

import argparse
import gzip
import json
import pathlib
import statistics

STAGES = ["remember", "recall-by-id", "recent-for-agent", "update", "mixed-90-10"]


def read(path):
    return json.loads(path.read_text())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=pathlib.Path)
    root = parser.parse_args().results
    complete, data = read(root / "complete.json"), read(root / "comparison.json")
    meta = read(root / "metadata.json")
    assert data["metadata"] == meta and data["audit"] == complete
    assert complete["all_value_checks_pass"] and complete["all_reopen_checks_pass"]
    groups, summaries = {}, []
    calls = readbacks = reopened = 0
    for variant in ["baseline", "optimized"]:
        paths = sorted((root / variant).glob("n*.summary.json"))
        expected = {
            f"n{n}-fast-r{r}-golsm.summary.json"
            for n in meta["sizes"]
            for r in range(1, meta["runs_per_size_and_revision"] + 1)
        }
        assert {p.name for p in paths} == expected
        for path in paths:
            s = read(path)
            prefix = s["prefix"]
            c = read(path.with_name(prefix + ".config.json"))
            raw = [
                json.loads(line)
                for line in path.with_name(prefix + ".jsonl").read_text().splitlines()
            ]
            stages = [r for r in raw if r["type"] == "stage"]
            resource = read(path.with_name(prefix + ".resources.json"))
            assert s["resources"] == resource and s["stages"] == stages
            assert [r["workload"] for r in stages] == STAGES
            n = s["base_records"]
            ops = 10000 if n == 10000 else 100000
            assert c["n"] == n and c["operations"] == c["write_operations"] == ops
            assert (
                c["mode"] == "fast" and c["backend"] == "golsm" and c["warmup"] == 100
            )
            assert c["k"] == 10 and c["seed"] == 20261016 and not c["verify_only"]
            assert (
                s["final_records"] == n + ops
                and raw[-1]["type"] == "complete"
                and raw[-1]["verified_records"] == n + ops
            )
            assert len(raw) == 6
            final = [
                json.loads(line)
                for line in path.with_name(prefix + ".reopen.jsonl")
                .read_text()
                .splitlines()
            ]
            assert final == [{"type": "reopen_verified", "verified_records": n + ops}]
            settings = resource["engine_settings"]
            assert (
                settings["engine_commit"] == meta["engine_revisions"][variant]
                and not settings["per_write_sync"]
            )
            assert (
                settings["flush_threshold"] == 1000
                and settings["cache_bytes"] == 16777216
            )
            assert (
                settings["compaction_interval_seconds"] == 30
                and settings["compaction_threshold"] == 4
            )
            assert (
                resource["cpus_allowed_list"] == "1"
                and resource["initial_engine_state"]["metrics"]["SSTables"] == 1
            )
            assert resource["preload"]["golsm_bulk_flush_threshold"] == n + 1
            for state in ["initial_engine_state", "final_engine_state"]:
                assert resource[state]["gomaxprocs"] == 1
                assert (
                    resource[state]["metrics"]["BackgroundError"]
                    == resource[state]["metrics"]["WriteError"]
                    == ""
                )
            with gzip.open(path.with_name(prefix + ".load.jsonl.gz"), "rt") as f:
                samples = [json.loads(line) for line in f]
            window = sorted(
                [
                    r
                    for r in samples
                    if resource["measurement_start_epoch"]
                    <= r["epoch"]
                    <= resource["measurement_end_epoch"]
                ]
                + resource["boundary_load_samples"],
                key=lambda r: r["epoch"],
            )
            gap = max(
                (b["epoch"] - a["epoch"] for a, b in zip(window, window[1:])), default=0
            )
            median_load = statistics.median(r["load"][0] for r in window)
            assert s["load"]["gate_pass"] == (median_load < 3 and gap <= 2.5)
            assert (
                s["load"]["median_load1"] == median_load
                and s["load"]["max_gap_seconds"] == gap
            )
            for stage in stages:
                assert stage["errors"] == 0 and stage["latency"]["count"] == ops
                groups.setdefault((n, stage["workload"], variant), []).append(stage)
                calls += stage["latency"]["count"]
                readbacks += stage["writes"]["count"]
            reopened += n + ops
            summaries.append((variant, s, raw[-1]))
    assert (
        len(summaries)
        == complete["completed"]
        == len(meta["sizes"]) * 2 * meta["runs_per_size_and_revision"]
    )
    assert calls == complete["timed_calls"]
    assert len(data["rows"]) == len(meta["sizes"]) * len(STAGES)
    assert {(row["base_records"], row["workload"]) for row in data["rows"]} == {
        (n, stage) for n in meta["sizes"] for stage in STAGES
    }
    for row in data["rows"]:
        for variant, values in row["variants"].items():
            stages = groups[row["base_records"], row["workload"], variant]
            for key in ["p50_ns", "p99_ns", "p999_ns", "max_ns"]:
                assert values[key] == statistics.median(
                    s["latency"][key] for s in stages
                )
            for key in ["verified_ops_s", "call_ops_s"]:
                assert values[key] == statistics.median(s[key] for s in stages)
    audit = {
        "verified_trials": len(summaries),
        "timed_calls": calls,
        "write_readbacks": readbacks,
        "records_after_new_process_reopen": reopened,
        "all_value_checks_pass": True,
        "all_reopen_checks_pass": True,
        "all_load_gates_pass": all(s["load"]["gate_pass"] for _, s, _ in summaries),
    }
    assert audit["all_load_gates_pass"] == complete["all_load_gates_pass"]
    (root / "audit.json").write_text(json.dumps(audit, indent=2) + "\n")
    text = "# GoLSM optimization results: alternating revisions\n\n"
    text += f"Baseline `{meta['engine_revisions']['baseline']}` → optimized `{meta['engine_revisions']['optimized']}`. Fast mode only, three repetitions per size and revision, same upstream W1–W5 workload on pancakes. No new Kiban/LMDB or durable performance measurements.\n\n"
    text += "Implementation changes, interpretation, and reproduction are in the [parent benchmark README](../../README.md).\n\n"
    text += f"Audited **{len(summaries)} trials**, **{calls:,} timed calls**, **{readbacks:,} write read-backs**, and **{reopened:,} records after new-process reopen**; no value errors.\n\n"
    text += (
        "All trials retained. "
        + (
            "Every trial passed the recorded load/coverage gate. "
            if audit["all_load_gates_pass"]
            else "Some trials failed the recorded load/coverage gate. "
        )
        + "These remain preliminary shared-VM measurements with three repetitions. Tables use medians of per-trial statistics; ratios are descriptive, not confidence intervals. Go bridge/copies remain inside call timing.\n\n"
    )
    for n in meta["sizes"]:
        text += f"## Base N={n:,}\n\n| Workload | Before p50 µs | After p50 µs | p50 speedup | Before verified ops/s | After verified ops/s | Throughput ratio |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n"
        for row in [r for r in data["rows"] if r["base_records"] == n]:
            b, a = row["variants"]["baseline"], row["variants"]["optimized"]
            text += f"| {row['workload']} | {b['p50_ns'] / 1000:.3f} | {a['p50_ns'] / 1000:.3f} | {b['p50_ns'] / a['p50_ns']:.2f}× | {b['verified_ops_s']:,.0f} | {a['verified_ops_s']:,.0f} | {a['verified_ops_s'] / b['verified_ops_s']:.2f}× |\n"
        text += "\n| Workload | Revision | p50 trial range µs | p99 µs | p999 µs | Median trial max µs | Largest observed µs |\n| --- | --- | ---: | ---: | ---: | ---: | ---: |\n"
        for row in [r for r in data["rows"] if r["base_records"] == n]:
            for variant, v in row["variants"].items():
                stages = groups[n, row["workload"], variant]
                lo, hi = v["p50_range_ns"]
                text += f"| {row['workload']} | {variant} | {lo / 1000:.3f}–{hi / 1000:.3f} | {v['p99_ns'] / 1000:.3f} | {v['p999_ns'] / 1000:.3f} | {v['max_ns'] / 1000:.3f} | {max(s['latency']['max_ns'] for s in stages) / 1000:.3f} |\n"
    text += "\n## Maintenance, final drain, and resources\n\nFinal sync/flush/full verification happens outside the foreground timer. The combined finish duration includes verification; it is not isolated maintenance CPU time. RSS is whole-process memory including the oracle/runtimes; peak RSS includes preload.\n\n| Base | Revision | Trial | Load median | Gate | Flushes | Compactions | Compaction seconds | Finish + verification seconds | Peak RSS MiB | Closed disk MiB |\n| ---: | --- | ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |\n"
    for variant, s, finish in summaries:
        r = s["resources"]
        m = r["final_engine_state"]["metrics"]
        text += f"| {s['base_records']:,} | {variant} | {s['trial']} | {s['load']['median_load1']:.2f} | {'pass' if s['load']['gate_pass'] else 'preliminary'} | {m['Flushes']} | {m['Compactions']} | {m['CompactionNanos'] / 1e9:.3f} | {finish['end_sync_flush_verify_seconds']:.3f} | {r['peak_rss_bytes'] / 2**20:.1f} | {s['disk_after_close']['apparent_bytes'] / 2**20:.1f} |\n"
    text += "\nThe optimized engine acknowledges threshold writes after WAL rotation and queues one immutable flush. It applies writer backpressure when the next memtable fills. Compaction construction no longer holds the global lock, and automatic jobs select similarly sized suffixes instead of rewriting the whole base. Those changes affect storage layout and where work occurs. Throughput includes generation/oracle/write read-backs, but excludes preload and final finish; this is not maintenance-drained saturation capacity.\n"
    (root / "REPORT.md").write_text(text)
    template = pathlib.Path(__file__).with_name("template.html").read_text()
    (root / "comparison.html").write_text(
        template.replace("/*DATA*/{}", json.dumps(data).replace("</", "<\\/"))
    )
    print(json.dumps(audit, indent=2))


if __name__ == "__main__":
    main()
