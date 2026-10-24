#!/usr/bin/env python3
"""Produce comparison data/Markdown only from a fully verified campaign."""

import argparse, collections, gzip, hashlib, json, pathlib, statistics

STAGES = ["remember", "recall-by-id", "recent-for-agent", "update", "mixed-90-10"]
ENGINES = ["kiban", "golsm", "lmdb"]


def median(xs):
    return statistics.median(xs)


def read(path):
    return json.loads(path.read_text())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=pathlib.Path)
    args = parser.parse_args()
    root = args.results
    complete = read(root / "complete.json")
    assert complete["all_reopen_verified"] and complete["zero_value_errors"]
    meta = read(root / "metadata.json")
    expected_prefixes = {
        f"n{n}-{mode}-r{trial}-{engine}"
        for n in meta["args"]["sizes"]
        for mode in ["durable", "fast"]
        for trial in range(
            1,
            (meta["args"]["small_runs"] if n == 10000 else meta["args"]["large_runs"])
            + 1,
        )
        for engine in meta["args"]["engines"]
    }
    assert {
        p.name.removesuffix(".summary.json") for p in root.glob("n*.summary.json")
    } == expected_prefixes
    for name, digest in meta["source_hashes"].items():
        assert (
            hashlib.sha256(
                (pathlib.Path(__file__).parent / name).read_bytes()
            ).hexdigest()
            == digest
        ), name
    summaries = []
    groups = collections.defaultdict(list)
    for path in sorted(root.glob("n*.summary.json")):
        s = read(path)
        prefix = s["prefix"]
        c = read(root / f"{prefix}.config.json")
        raw = [
            json.loads(x) for x in (root / f"{prefix}.jsonl").read_text().splitlines()
        ]
        stages = [x for x in raw if x["type"] == "stage"]
        reopen = [
            json.loads(x)
            for x in (root / f"{prefix}.reopen.jsonl").read_text().splitlines()
        ]
        assert path.name == prefix + ".summary.json"
        assert c["backend"] == s["engine"] and c["mode"] == s["mode"]
        assert c["n"] == s["base_records"] and c["seed"] == 20261016 and c["k"] == 10
        assert c["warmup"] == 100 and not c["verify_only"]
        assert (
            c["operations"] == s["operations"] == (10000 if c["n"] == 10000 else 100000)
        )
        assert (
            c["write_operations"]
            == s["write_operations"]
            == (
                min(c["operations"], 10000)
                if c["mode"] == "durable"
                else c["operations"]
            )
        )
        assert s["final_records"] == c["n"] + c["write_operations"]
        assert s["stages"] == stages
        finished = [r for r in raw if r["type"] == "complete"]
        assert (
            len(raw) == 6
            and len(finished) == 1
            and finished[0]["verified_records"] == s["final_records"]
        )
        resource = read(root / f"{prefix}.resources.json")
        assert s["resources"] == resource
        with gzip.open(root / f"{prefix}.load.jsonl.gz", "rt") as stream:
            samples = [json.loads(line) for line in stream]
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
        load = statistics.median(r["load"][0] for r in window)
        assert s["load"] == {
            "samples": len(window),
            "max_gap_seconds": gap,
            "median_load1": load,
            "max_load1": max(r["load"][0] for r in window),
            "gate_pass": load < 3 and gap <= 2.5,
        }
        settings = resource["engine_settings"]
        if s["engine"] in ("golsm", "kiban"):
            assert settings["per_write_sync"] == (c["mode"] == "durable")
        if s["engine"] == "golsm":
            assert (
                settings["flush_threshold"] == 1000
                and settings["cache_bytes"] == 16777216
            )
            assert (
                settings["compaction_interval_seconds"] == 30
                and settings["compaction_threshold"] == 4
            )
            assert settings["engine_commit"] == meta["engine_versions"]["golsm"]
            assert resource["preload"]["golsm_bulk_flush_threshold"] == c["n"] + 1
            assert resource["initial_engine_state"]["metrics"]["SSTables"] == 1
            for state in ["initial_engine_state", "final_engine_state"]:
                assert resource[state]["gomaxprocs"] == 1
                assert (
                    resource[state]["metrics"]["BackgroundError"]
                    == resource[state]["metrics"]["WriteError"]
                    == ""
                )
        assert [r["workload"] for r in stages] == STAGES
        assert len(reopen) == 1 and reopen[0] == {
            "type": "reopen_verified",
            "verified_records": c["n"] + c["write_operations"],
        }
        for i, r in enumerate(stages):
            assert r["errors"] == 0 and r["latency"]["count"] == (
                c["write_operations"] if i in (0, 3) else c["operations"]
            )
        assert s["resources"]["cpus_allowed_list"] == meta["engine_cpus"]
        if s["engine"] == "lmdb":
            assert s["resources"]["engine_settings"]["lmdb_flags_bits"] == (
                0 if s["mode"] == "durable" else 0x50000
            )
        assert s["reopen_verified"]
        summaries.append(s)
        for r in stages:
            groups[(s["base_records"], s["mode"], r["workload"], s["engine"])].append(
                (r, s)
            )
    expected = sum(
        (meta["args"]["small_runs"] if n == 10000 else meta["args"]["large_runs"])
        * 2
        * len(meta["args"]["engines"])
        for n in meta["args"]["sizes"]
    )
    assert len(summaries) == complete["completed"] == expected
    audit = {
        "verified_trials": len(summaries),
        "timed_calls": sum(
            r["latency"]["count"] for s in summaries for r in s["stages"]
        ),
        "write_readbacks": sum(
            r["writes"]["count"] for s in summaries for r in s["stages"]
        ),
        "records_checked_after_new_process_reopen": sum(
            s["final_records"] for s in summaries
        ),
        "all_value_checks_pass": True,
        "all_reopen_checks_pass": True,
        "all_load_gates_pass": all(s["load"]["gate_pass"] for s in summaries),
    }
    (root / "audit.json").write_text(json.dumps(audit, indent=2) + "\n")
    aggregated = []
    text = "# GoLSM vs Kiban vs LMDB: matched engine workload\n\n"
    text += "All engines ran in one release executable with in-process calls on pancakes. These are new measurements, not the post’s hardware results. GoLSM uses a Go C archive; its measured latency includes the foreign-function call and result copy. The original Kiban/LMDB adapters and the upstream generator, oracle, and histogram are retained.\n\n"
    text += f"Validated **{audit['verified_trials']} trials**, **{audit['timed_calls']:,} timed calls**, **{audit['write_readbacks']:,} immediate write read-backs**, and **{audit['records_checked_after_new_process_reopen']:,} records after new-process reopen**. No value errors.\n\n"
    text += "The original direct Kiban handle buffers its WAL and defers explicit flushing to the excluded finish step; GoLSM retains its automatic 1,000-operation flush and background compactor. Thus fast p50 is foreground acknowledgment latency with different buffering/maintenance policies, not equal sustained compaction-drained capacity. GoLSM also pays its measured C-archive bridge overhead.\n\n"
    text += "Cells are medians of trial statistics, with trial ranges where shown. Latency histogram quantization is under 3.125%. No trials were dropped. These are shared-VM results; the 1M results remain preliminary with three repetitions. Full settings and limitations are in the benchmark README.\n"
    for n in sorted(set(k[0] for k in groups)):
        for mode in ["durable", "fast"]:
            text += f"\n## Base N={n:,}, {mode}\n\n"
            failed = any(
                not s["load"]["gate_pass"]
                for s in summaries
                if s["base_records"] == n and s["mode"] == mode
            )
            text += (
                "**Preliminary: one or more trials exceeded the retained load/coverage gate. All trials are included.**\n\n"
                if failed
                else (
                    "**Preliminary: three repetitions on a shared VM.**\n\n"
                    if n == 1000000
                    else "Shared VM; recorded load/coverage gate passed for all trials in this subset.\n\n"
                )
            )
            text += "| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |\n| --- | ---: | ---: | ---: | ---: | ---: |\n"
            for workload in STAGES:
                row = {
                    "base_records": n,
                    "mode": mode,
                    "workload": workload,
                    "engines": {},
                }
                for engine in ENGINES:
                    rs = groups[n, mode, workload, engine]
                    r = [x[0] for x in rs]
                    values = {"trials": len(r)}
                    for field in ["p50_ns", "p99_ns", "p999_ns", "max_ns"]:
                        xs = [
                            x["latency"][field]
                            for x in r
                            if x["latency"][field] is not None
                        ]
                        values[field] = median(xs) if xs else None
                        values[field + "_min"] = min(xs) if xs else None
                        values[field + "_max"] = max(xs) if xs else None
                    for field in ["verified_ops_s", "call_ops_s"]:
                        values[field] = median(x[field] for x in r)
                    values["all_load_gates_pass"] = all(
                        x[1]["load"]["gate_pass"] for x in rs
                    )
                    row["engines"][engine] = values
                go = row["engines"]["golsm"]["p50_ns"]
                row["kiban_over_golsm"] = row["engines"]["kiban"]["p50_ns"] / go
                row["lmdb_over_golsm"] = row["engines"]["lmdb"]["p50_ns"] / go
                aggregated.append(row)
                cells = [
                    f"{row['engines'][e]['p50_ns'] / 1000:.3f} [{row['engines'][e]['p50_ns_min'] / 1000:.3f}, {row['engines'][e]['p50_ns_max'] / 1000:.3f}]"
                    for e in ENGINES
                ]
                text += f"| {workload} | {' | '.join(cells)} | {row['kiban_over_golsm']:.3f}× | {row['lmdb_over_golsm']:.3f}× |\n"
            text += "\nRatios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.\n\n"
            text += "| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |\n| --- | --- | ---: | ---: | ---: | ---: | ---: |\n"
            for row in [
                a for a in aggregated if a["base_records"] == n and a["mode"] == mode
            ]:
                for engine, v in row["engines"].items():
                    p999 = (
                        f"{v['p999_ns'] / 1000:.3f}"
                        if v["p999_ns"] is not None
                        else "—"
                    )
                    text += f"| {engine} | {row['workload']} | {v['p99_ns'] / 1000:.3f} | {p999} | {v['max_ns'] / 1000:.3f} | {v['verified_ops_s']:,.0f} | {v['call_ops_s']:,.0f} |\n"

    text += "\n## GoLSM maintenance counters\n\nThese counters are scoped to the foreground-open database instance, including its initial and final verification; compaction may overlap blocked API calls. They are not an additive CPU-time profile.\n\n| Base N | Mode | Compactions | Total compaction seconds | Flushes |\n| ---: | --- | ---: | ---: | ---: |\n"
    for n in sorted(set(s["base_records"] for s in summaries)):
        for mode in ["durable", "fast"]:
            ms = [
                s["resources"]["final_engine_state"]["metrics"]
                for s in summaries
                if s["base_records"] == n
                and s["mode"] == mode
                and s["engine"] == "golsm"
            ]
            if ms:
                text += f"| {n:,} | {mode} | {median(m['Compactions'] for m in ms):g} | {median(m['CompactionNanos'] for m in ms) / 1e9:.3f} [{min(m['CompactionNanos'] for m in ms) / 1e9:.3f}, {max(m['CompactionNanos'] for m in ms) / 1e9:.3f}] | {median(m['Flushes'] for m in ms):g} |\n"
    text += "\n## Trial loads and resources\n\n| Base N | Mode | Trial | Engine | Load median / max | Gate | End RSS MiB | Peak RSS MiB | Disk apparent MiB | Disk allocated MiB |\n| ---: | --- | ---: | --- | ---: | --- | ---: | ---: | ---: | ---: |\n"
    for s in sorted(
        summaries, key=lambda s: (s["base_records"], s["trial"], s["mode"], s["engine"])
    ):
        r = s["resources"]
        text += f"| {s['base_records']:,} | {s['mode']} | {s['trial']} | {s['engine']} | {s['load']['median_load1']:.2f} / {s['load']['max_load1']:.2f} | {'pass' if s['load']['gate_pass'] else 'preliminary'} | {r['rss_bytes'] / 2**20:.1f} | {r['peak_rss_bytes'] / 2**20:.1f} | {s['disk_after_close']['apparent_bytes'] / 2**20:.1f} | {s['disk_after_close']['allocated_bytes'] / 2**20:.1f} |\n"
    (root / "comparison.json").write_text(
        json.dumps(
            {
                "audit": audit,
                "metadata": meta,
                "calibration": read(root / "calibration.json"),
                "rows": aggregated,
            },
            indent=2,
        )
        + "\n"
    )
    (root / "REPORT.md").write_text(text)
    print(json.dumps(audit, indent=2))


if __name__ == "__main__":
    main()
