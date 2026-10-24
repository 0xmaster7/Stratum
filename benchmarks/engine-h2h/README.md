# Matched GoLSM / Kiban / LMDB benchmark

This harness reproduces the agent-memory engine workload behind [Kiban's head-to-head post](https://github.com/aetosdios27/kiban/blob/082858f86fc38b79e582eb34b7f39aa2368b3af0/validation/ENGINE_H2H.md), adding GoLSM to the same in-process Rust driver. It measures new results on `pancakes`; it does not reuse the post's latency numbers or imply matching hardware.

## Pinned inputs

| Input | Revision/version |
| --- | --- |
| GoLSM | `b9a1c07a85be14eabdcba6dc129bfeaf7a59a82a` |
| Kiban engine cited by the post | `361305a8b63fef309e301e8106c254e60a70ac84` |
| Upstream workload/engine harness | `082858f86fc38b79e582eb34b7f39aa2368b3af0` |
| LMDB implementation | `lmdb-master3-sys 0.2.5` through `heed3 0.22.0` |
| Rust allocator | `mimalloc 0.1.48`, `libmimalloc-sys 0.1.44` |
| Stored JSON codec | `serde 1.0.228`, `serde_json 1.0.143` |

The post's cited Kiban engine is intentionally pinned. Its current GitHub head includes later read-path changes, including a default perfect-hash index, so these results must not be presented as a measurement of today's Kiban head. The engine source is not patched. Vendored upstream files are attributed under `UPSTREAM-LICENSE`; their generator, oracle, histogram, model, random function, and Kiban adapter are byte-for-byte copies from the harness revision. `src/main.rs` retains its run flow with GoLSM registration, calibration, and extra Go metrics. `src/backend.rs` retains its raw-LMDB adapter and adds the Go bridge; Helix is outside this requested comparison.

The Rust release profile matches the post: opt-level 2, LTO, one codegen unit, panic abort, debug information retained. The same release executable selects each engine at runtime, and its SHA-256 is checked before every trial. Kiban and LMDB share MiMalloc. GoLSM uses the standard optimized Go compiler and its own Go allocator/GC.

## What matches the post

- One process and in-process engine calls. No HTTP, sockets, routing, request transport, or subprocess call per operation.
- Seed `20261016`, 64 agents, 200 ASCII text bytes plus the same five metadata/value fields, and latest K=10.
- Identical ordered primary key, JSON value bytes, deterministic operation/key sequence, output model, histogram, correctness oracle, and W1–W5 order for all three engines.
- A discarded 100-record warmup, untimed preload, verification of every base record, close/reopen, and another full base verification before timing.
- Timed W1 fresh inserts, W2 point reads by ID, W3 latest-for-agent, W4 overwrites, then W5 deterministic 90/10 point-read/update mix. Every write is immediately read back and checked outside the call timer. Read results and ordered lists are checked too.
- Explicit finish/sync outside foreground measurement, full final verification, orderly close, and another full verification in a new process.
- Five alternating repetitions at base N=10,000 and three at base N=1,000,000. No engines run concurrently. Each repetition runs durable Kiban/GoLSM/LMDB, then fast Kiban/GoLSM/LMDB.

| Base population | Mode | W1 inserts | W2/W3/W5 calls each | W4 overwrites | Final population |
| ---: | --- | ---: | ---: | ---: | ---: |
| 10,000 | durable | 10,000 | 10,000 | 10,000 | 20,000 |
| 10,000 | fast | 10,000 | 10,000 | 10,000 | 20,000 |
| 1,000,000 | durable | 10,000 | 100,000 | 10,000 | 1,010,000 |
| 1,000,000 | fast | 100,000 | 100,000 | 100,000 | 1,100,000 |

The large durable mixed stage still contains 10,000 writes. The timed write cap does not reduce the population or the point/range call count. Initial N and final population are different quantities.

## Durability and preloading

| Mode | Kiban | GoLSM | LMDB |
| --- | --- | --- | --- |
| durable | `put` then `sync()` per timed write, including WAL `sync_data` | Existing `Put` with `SyncWrites: true`, including WAL `fsync` | One synchronous write transaction/commit per call; environment flags `0x0` |
| fast | No per-write `sync`; maintenance may synchronize | Existing `Put` with `SyncWrites: false`; threshold flushes/background compaction may synchronize | `NO_SYNC | NO_META_SYNC`, flags `0x50000`; one transaction/commit per call |

These match acknowledged-write durability intent, not syscall identity: LMDB persists copy-on-write pages and metadata; Kiban syncs a WAL with `fdatasync`; GoLSM uses `fsync`. Final explicit sync and successful fast-mode reopen are not a power-loss test.

The original bulk preload uses Kiban WriteBatch chunks and one relaxed LMDB transaction. GoLSM has no batch API. Its untimed preload uses ordinary buffered `Put` calls with `FlushThreshold=N+1`, then a final `Flush`, producing one sorted base table. The preload instance is closed; every foreground instance reopens with the normal default flush threshold of 1,000. This prevents preload-only automatic flushing from imposing an unrelated thousand-table starting layout at 1M. No timed write receives that larger threshold, and the timed GoLSM read cache/compactor are not retuned. Initial Go state and the preload threshold are recorded and audited.

GoLSM foreground settings: default 16 MiB estimated LRU budget, 1,000-operation flush threshold, 30-second compactor check, four-table trigger. Kiban uses the post's default options, including its 32 MiB block cache. Cache types and capacities differ; this is a default-configuration workload comparison, not an equal-total-memory experiment. LMDB uses the original 10 GiB map, 200 database slots, and 200 reader slots; `WRITEMAP` and `MAP_ASYNC` remain off.

## Cross-language boundary

GoLSM's unchanged engine files are compiled as a Go **C archive** and statically linked into the Rust executable. The Rust GoLSM adapter serializes with the same Rust JSON codec as Kiban/LMDB, calls the Go engine, and deserializes with that same codec. The Go bridge copies incoming keys/values, returns C-owned buffers, and frees them through libc. Go range results are length-framed byte buffers, not an HTTP or JSON transport envelope. All this bridge work is **inside GoLSM's call timer**.

The excluded calibration measures a Go no-op and a 291-byte echo/copy/free path for 100,000 calls. Its recorded overhead provides context; no constant is subtracted from measured latency. The echo is not a complete model of every real call. This is an in-process Go embedding comparison, not a claim of zero-cost Rust-to-Go calls or a standalone native-Go timing campaign. The linked Go runtime is present in all three engine processes; GoLSM also exercises its own heap/GC during operations.

## Host, affinity, and load

`pancakes` has two logical vCPUs on an AMD EPYC 7763 host and approximately 8 GiB RAM, running Linux on Azure. The engine process and its threads are pinned to **CPU 1**; the observer/driver uses **CPU 0**. Go reports `GOMAXPROCS=1`, which is audited. CPU affinity does not reserve the underlying physical core or remove other services. The original post used a different desktop, NVMe/filesystem, compiler release, RAM size, and two physical P-cores plus SMT. Its absolute numbers and speed ratios cannot be transferred to this host.

The driver samples load every 200 ms plus engine-side boundaries, retains all trials, and checks the post's median one-minute load <3 and maximum sampling gap ≤2.5 seconds. The original absolute load threshold is retained for audit continuity; it is not a portable proof that this smaller shared VM is idle. The recorded interval excludes warmup/preload/open and includes final in-process verification. No loaded trial is selectively replaced. The 1M campaign remains preliminary because three repetitions on a shared VM provide limited confidence.

## Reproduce

Requirements: Linux, Go 1.25+, a Rust toolchain supporting edition 2024, a C compiler, Git, Python 3, and `taskset`. The recorded run uses Rust/Cargo 1.99.1 and Go 1.25.1. There are no root/package-manager steps in the scripts.

From the repository root:

```sh
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"
benchmarks/engine-h2h/build.sh
cd benchmarks/engine-h2h
cargo test --release --locked -- --test-threads=1
python3 run.py --output work/my-fresh-run
python3 report.py work/my-fresh-run
```

The output directory must be fresh. The default run performs the full 48-trial protocol above and can take over an hour when durable syncs are slow. `--sizes 10000` runs only the smaller matrix. `--small-runs` and `--large-runs` can produce a shorter explicitly different protocol. CPU selectors must identify available CPUs on the target machine; the recorded comparison uses one engine CPU.

The build script fetches the pinned Kiban source, extracts GoLSM's pinned engine files from Git, compiles the Go archive, and builds Rust using the checked-in lockfile. It does not modify either storage engine. Databases are temporary per-trial directories and are removed only after successful new-process verification, unless `--keep-databases` is supplied. Existing user databases are not opened. Failed/incomplete runs lack `complete.json`; the report generator refuses to publish a completed comparison from them.

Raw outputs include per-stage JSONL, process/reopen configs and logs, load samples, engine settings and Go metrics, RSS, filesystem size after close, command lines, source/binary hashes, and a completion marker. `report.py` independently checks recorded counts, flags, affinity, and reopen markers before producing `audit.json`, `comparison.json`, and `REPORT.md`. Full-value correctness is enforced during execution; a JSON audit is not an independent reread of deleted temporary databases.

## Reading the results

P50/p99/p999 and maximum call latency include key construction, stored-value serialization/deserialization, engine work, and owned result creation. They exclude data generation, the oracle, and the separate write read-back. Verified throughput includes that surrounding work; call-only throughput divides by summed call time. Neither is a concurrent-writer saturation test. Histogram percentiles have less than 3.125% bucket error. Reported aggregates are medians of per-trial statistics, not percentiles pooled across trials.

Fast acknowledgment latency can hide deferred maintenance. GoLSM's flush/compaction/scan lock and Kiban's flush/compaction policy are part of their measured behavior. P50 does not capture rare flush pauses; inspect p99/p999/max and verified throughput. These are warm, resident-dataset workloads, not cold/out-of-RAM tests. No cache dropping, equal-memory cache sweep, forced compaction/vacuum, or hardware power-failure test is performed.

RSS covers the complete process, including runtime, allocator, oracle, bulk preload, and resident mapped pages; peak RSS includes preload. It is not an engine heap-only measurement. Disk apparent/allocated size is measured after close without forcing every engine to a minimum-size state; it is not measured device write amplification.

An initial interrupted setup pilot is retained under the ignored work directory and excluded from the comparison. The final campaign uses a fresh directory and one frozen binary after the bulk-preload method was aligned; no completed trial was selected for a favorable result.
