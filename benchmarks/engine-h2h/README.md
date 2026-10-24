# Matched GoLSM / Kiban / LMDB benchmark

This harness reproduces the agent-memory engine workload behind [Kiban's head-to-head post](https://github.com/aetosdios27/kiban/blob/082858f86fc38b79e582eb34b7f39aa2368b3af0/validation/ENGINE_H2H.md), adding GoLSM to the same in-process Rust driver. It measures new results on `pancakes`; it does not reuse the post's latency numbers or imply matching hardware.

The recorded three-engine comparison below measures GoLSM `b9a1c07`. Later engine optimizations are evaluated separately in [`../optimizations/`](../optimizations/); those changes do not retroactively update these numbers.

## Recorded comparison: October 24, 2026

The complete [report](results/2026-10-24/REPORT.md), [interactive comparison](results/2026-10-24/comparison.html), [audit](results/2026-10-24/audit.json), and [raw trial files](results/2026-10-24/) are checked in. All **48 trials**, **8.88 million timed calls**, **2.79 million immediate write read-backs**, and **19.59 million records checked after new-process reopen** passed value validation. The campaign ran from 07:43 to 09:16 UTC on pancakes.

These are preliminary measurements: some 10k trials failed the retained load gate; all are included. All 1M trials passed that gate, but have only three repetitions on a shared VM. Ratios below compare medians, with full trial ranges and tails in the report.

- **Durable writes:** GoLSM's insert/overwrite p50 is 1.05–1.12× faster than Kiban at 10k, and 1.36–1.38× at 1M. Against LMDB, it is 1.18–1.23× at 10k and 1.21–1.35× at 1M. At 1M, however, verified insert/overwrite throughput is about **166 ops/s for GoLSM versus 166–170 for Kiban**; lower p50 does not establish higher sustained throughput.
- **Point reads:** at 10k, GoLSM is 2.6–4.0× slower than the competitors across modes. At 1M durable it is 1.87× faster than Kiban, but 2.14× slower than LMDB. At 1M fast it is 1.81× slower than Kiban and 7.48× slower than LMDB. The read stages follow inserts, so mode changes the accumulated storage layout too.
- **Latest 10:** GoLSM loses to both engines in every subset. Its p50 is 3.7–6.1× slower than Kiban and 5.1–11.7× slower than LMDB. Its largest observed per-trial scan maximum in the 1M fast trials exceeds five seconds. Sorting the memtable for each scan and blocking maintenance are profiling candidates; these data do not isolate their individual costs.
- **Fast writes:** GoLSM's p50 is 3.1–6.6× faster than LMDB, while Kiban is 1.3–2.3× faster than GoLSM. At 1M, verified insert throughput is **25.1k / 240.0k / 57.7k ops/s** for GoLSM / Kiban / LMDB. GoLSM pays automatic flushes; Kiban's direct API defers its flush. LMDB's 1M fast overwrites also have a large tail: median p50 **19.97 µs**, p99 **19.40 ms**, verified throughput **702 ops/s**. Fast p50 alone is especially misleading here.

The excluded bridge calibration is **0.192 µs p50** for a 291-byte echo/copy/free call; this overhead remains included, with no subtraction. It is too small by itself to explain the multi-microsecond read gaps, but it does not model all per-call Go allocation/GC costs.

The [separate syscall diagnostic](results/2026-10-24/sync-diagnostic/summary.json) passed for all engines and modes. Each run has 2,100 timed writes; counts below cover the **whole process**, including discarded warmup, preload, finish, and close. Its instrumented latencies are excluded from the comparison.

| Engine | Durable kernel sync calls | Fast kernel sync calls |
| --- | ---: | ---: |
| Kiban | 2,316 `fdatasync` + 32 `fsync` | 6 `fdatasync` + 32 `fsync` |
| GoLSM | 2,349 `fsync` | 39 `fsync` |
| LMDB | 2,315 `fdatasync` | 5 `fdatasync` |

The post's 2.2–2.4× Kiban/LMDB durable-write claim is not reproduced as a p50 ratio on this VM: the new ratio is approximately **0.89–1.12×** across the four insert/overwrite subsets. This is a different host and storage stack, so it does not invalidate that author's hardware-specific measurements. This campaign measures the post's pinned engine version, not newer Kiban read optimizations.

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

## Storage-policy differences that matter here

This protocol measures foreground acknowledgments, not equal maintenance-drained capacity. The direct `Kiban` API used by the original adapter buffers WAL writes and requires an explicit flush; its configured write-buffer trigger applies to `SharedKiban`, not this direct handle. Pending timed records can therefore remain in its memtable until the excluded final `finish`. GoLSM's timed default still automatically flushes every 1,000 operations and can run its background compactor. Those costs and differences in read topology are included in the results. Neither engine is force-drained to the same work boundary after each call.

GoLSM writes each WAL record directly through `os.File.Write`; Kiban uses a userspace-buffered WAL. In fast mode, removing per-write sync does not make those buffering paths or their process-crash behavior identical. LMDB still performs a copy-on-write transaction commit. These are concrete implementation differences; a latency table alone does not assign a percentage of the gap to any one of them.

| Engine | Read path / storage | Relevant capabilities |
| --- | --- | --- |
| GoLSM `b9a1c07` | Slice-chained sharded hash memtable, per-record checked SSTables, resident sparse indexes, mmap and a 16 MiB read LRU | Persistent deletes, full-table compaction, materialized range scans; no MVCC snapshots, atomic multi-key batches, or transactions |
| Kiban `361305a` | Ordered/versioned memtable, checked SST blocks, Bloom filters and a default 32 MiB block cache; mmap optional and off | Sequence snapshots, atomic WAL batches, streaming iterators, leveled compaction, a separate shared concurrent API |
| LMDB via `heed3` | mmap-backed copy-on-write B+tree; reads use a read transaction and decode the stored JSON | MVCC transactions and page-level tree updates; no LSM flush/compaction path |

GoLSM's `Scan` currently snapshots and sorts memtable entries for each call, then merges SSTable cursors. Its timer-driven compaction can change the pending memtable/table layout between stages and trials. This is an important profiling candidate when range latency varies, rather than proof of a measured CPU-time breakdown. The single-client protocol does not measure GoLSM's sharding benefit, Kiban's shared writer throughput, or LMDB's concurrent-reader scaling.

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

The build defaults to the historical GoLSM pin. To measure another committed engine revision, set `GOLSM_REF=<commit>` when invoking `build.sh`; the build and runtime settings record the resolved commit, and optional engine files are selected from that same revision. The alternating GoLSM-only follow-up uses `compare_revisions.py --output work/followup --before-binary <saved-original-binary>` after building the optimized revision. Its default is three repetitions at each size in **fast mode only**, and it retains all trials. It does not rerun Kiban/LMDB or claim new durable performance numbers.

The build script fetches the pinned Kiban source, extracts GoLSM's pinned engine files from Git, compiles the Go archive, and builds Rust using the checked-in lockfile. It does not modify either storage engine. Databases are temporary per-trial directories and are removed only after successful new-process verification, unless `--keep-databases` is supplied. Existing user databases are not opened. Failed/incomplete runs lack `complete.json`; the report generator refuses to publish a completed comparison from them.

Raw outputs include per-stage JSONL, process/reopen configs and logs, load samples, engine settings and Go metrics, RSS, filesystem size after close, command lines, source/binary hashes, and a completion marker. `report.py` independently checks recorded counts, flags, affinity, and reopen markers before producing `audit.json`, `comparison.json`, and `REPORT.md`. Full-value correctness is enforced during execution; a JSON audit is not an independent reread of deleted temporary databases.

## Reading the results

P50/p99/p999 and maximum call latency include key construction, stored-value serialization/deserialization, engine work, and owned result creation. They exclude data generation, the oracle, and the separate write read-back. Verified throughput includes that surrounding work; call-only throughput divides by summed call time. Neither is a concurrent-writer saturation test. Histogram percentiles have less than 3.125% bucket error. Reported aggregates are medians of per-trial statistics, not percentiles pooled across trials.

Fast acknowledgment latency can hide deferred maintenance. GoLSM's flush/compaction/scan lock and Kiban's flush/compaction policy are part of their measured behavior. P50 does not capture rare flush pauses; inspect p99/p999/max and verified throughput. These are warm, resident-dataset workloads, not cold/out-of-RAM tests. No cache dropping, equal-memory cache sweep, forced compaction/vacuum, or hardware power-failure test is performed.

RSS covers the complete process, including runtime, allocator, oracle, bulk preload, and resident mapped pages; peak RSS includes preload. It is not an engine heap-only measurement. Disk apparent/allocated size is measured after close without forcing every engine to a minimum-size state; it is not measured device write amplification.

An initial interrupted setup pilot is retained under the ignored work directory and excluded from the comparison. The final campaign uses a fresh directory and one frozen binary after the bulk-preload method was aligned; no completed trial was selected for a favorable result.

The optional excluded diagnostic confirms kernel sync calls for all three engines, including Go's direct syscalls (which a libc-only interposer would miss):

```sh
python3 sync_diagnostic.py work/my-fresh-run
```

It requires `strace`, runs after the main campaign, and uses the post's 10k untimed base with 1,000 calls per workload (2,100 timed writes). Instrumented latency is excluded from the comparison. Its kernel counts include warmup/preload/finish/close; LMDB's synchronous metadata `pwrite` path is additional to the sync syscall counts. It is an observation of the configured persistence paths, not a power-loss certification.
