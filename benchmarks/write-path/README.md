# Recovering GoLSM fast-write throughput

This follow-up profiles and addresses the write-throughput regression in engine `5d13cf5`, while retaining its read/scan improvements. It compares that revision against **`37abd15`** using the same in-process Rust/Go JSON workload. Each change was committed separately:

| Commit | Change |
| --- | --- |
| `cf0a523` | Remove directory syncs after deleting files already excluded by a durable manifest/checkpoint |
| `17812ef` | Rotate between uniquely named active WAL generations; migrate legacy `data.log` once; sync the new name before use without syncing an empty payload |
| `37abd15` | Build sparse indexes and Bloom filters while streaming SSTable output; buffer index writes and avoid rereading newly written tables |

## Persistence ordering

Data/index files are still synced and their directory entries persisted **before** publishing the replacement manifest. The manifest and its directory rename are still synced before a frozen memtable is released or old files are removed. Each durable mutation still calls WAL `fsync`. Rotation still syncs the previous WAL and persists the next filename before writers can use it; the first durable record syncs the new WAL's contents. Fast mode still uses ordinary WAL writes and relies on rotation, explicit flush, and close for syncing.

Unlinking an obsolete file does not need another directory sync for correctness: if that deletion is lost across a crash, recovery ignores the file using the already durable manifest/checkpoint. A new test recreates corrupt obsolete WAL/SSTable/index files and checks that they are ignored and removed, without resurrecting old values or deleted keys.

The highest uncheckpointed numbered WAL is the active log when `data.log` is absent. Legacy `data.log`, when present, is replayed after numbered segments because older revisions used it for their newest writes. Recovery rejects duplicate generations and truncates incomplete tails. Tests cover legacy migration, multiple generations, partial active tails, and subprocess exits at rotation/publication boundaries. SSTable creation validates ordering and preserves the existing checked record/index format. Open still validates/rebuilds persisted indexes; checksum validation during point reads is unchanged.

## Excluded diagnostics

The diagnostic benchmark `BenchmarkMaintenanceWritePath` writes 291-byte values over 10k keys with normal background maintenance, and includes a final `Flush` in its timer. CPU/block/mutex profiles cover the entire test process. Syscall traces use a separate 20k-write invocation and include Go's benchmark calibration, startup, drain, and close. Their latencies are instrumented and are not used as the matched-workload comparison.

Raw outputs, profiles, and compressed syscall traces are retained under [`2026-10-25/before/`](2026-10-25/before/) and [`2026-10-25/after/`](2026-10-25/after/). The before profile uses engine code from `5d13cf5` plus the benchmark added in `dda275c`; the after profile uses `37abd15`. Both run on pancakes, CPU 1, `GOMAXPROCS=1`, Go 1.25.1. An initial setup run used a stale diagnostic checkout and was discarded before collecting these recorded profiles.

In the 100k-write profiled diagnostic, drained time fell **78.379 → 51.870 µs/op**, allocations **23 → 12/op**, and allocated bytes **4,701 → 2,720/op**. Both invocations reported 100 flushes and 32 compactions. The before block profile attributed about **4.62 seconds** to writer queue waiting; that is an observation of this profiled invocation, not a general latency distribution. Block delays across goroutines are not additive wall time. CPU profiling also identified the second SSTable read/index-building pass; it is now avoided for freshly written output.

The 20k-write traced invocations made **250 → 184 `fsync` calls** (26% fewer): directory syncs **124 → 79**, WAL syncs **46 → 25**, with manifest/data/index sync counts unchanged. The test-process traced time sums were 2.19/1.21 seconds, but instrumentation and shared-host variability prevent treating that as an uninstrumented latency ratio. The after block profile recorded **3.33 seconds** of queue waiting; the queue still matters.

## Matched-workload results

The [complete report](2026-10-25/matched-fast/REPORT.md), [interactive comparison](2026-10-25/matched-fast/comparison.html), [audit](2026-10-25/matched-fast/audit.json), and raw trial files are checked in. The final campaign ran **17:07–17:14 UTC on October 25, 2026**. All **12 trials**, **3.3M timed calls**, **1.386M immediate write read-backs**, and **6.72M records checked after new-process reopen** passed value/reopen validation.

| Base records | Operation | Before verified ops/s | After verified ops/s | Change |
| ---: | --- | ---: | ---: | ---: |
| 10,000 | Insert | 16,297 | 20,024 | +23% |
| 10,000 | Overwrite | 13,909 | 16,583 | +19% |
| 1,000,000 | Insert | 12,884 | 15,301 | +19% |
| 1,000,000 | Overwrite | 11,298 | 13,820 | +22% |

These are medians across three paired repetitions. At 1M, point-read p50 was essentially unchanged (**4.032 → 4.096 µs**) and latest-10 throughput rose **20.0k → 20.7k ops/s**. At 10k, point-read throughput rose **19%**, mixed throughput rose **21%**, and latest-10 throughput fell **13%**. The small write p50 values did not improve; verified throughput captures the avoided maintenance work better than p50.

Every 10k trial passed the recorded load/coverage gate. **Five of six 1M trials failed it**: before medians were 1.38/4.88/3.77, after medians 7.62/7.96/3.24. Other compute processes were observed on the VM during the large phase. All trials remain included; no failed trial was selectively replaced. The optimized first 1M trial recorded a **7.34-second overwrite maximum** during its load spike. This campaign does not establish a stable tail-latency improvement or fully recover the original engine's write performance. Cross-day absolute comparisons to the earlier campaign are unsuitable.

These changes reduce actual sync/allocation work and partially recover paired write throughput while retaining the large-read behavior in these samples. Queue waiting remains substantial, and the 1M performance ratios remain preliminary. Durable behavior is covered by tests, with no new durable performance claim. Validation logs are in [`2026-10-25/validation/`](2026-10-25/validation/).

## Reproduce

On pancakes, from the repository root (using a saved binary from the completed previous campaign):

```sh
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"
GOLSM_REF=37abd15 benchmarks/engine-h2h/build.sh
cd benchmarks/engine-h2h
cargo test --release --locked -- --test-threads=1
python3 compare_revisions.py --output work/fresh-write-recovery \
  --before-binary work/read-optimized-5d13cf5.bin \
  --baseline-metadata ../optimizations/2026-10-24/matched-fast/metadata.json \
  --baseline-variant optimized
```

The saved baseline binary's hash must match its completed campaign. For a new build/toolchain, first record a new completed baseline provenance campaign as described in [`../optimizations/README.md`](../optimizations/README.md#reproduction). The new comparison alternates revision order, runs three repetitions at each of 10k and 1M base records in fast mode only, retains every trial, and performs immediate full-value write read-backs and final new-process verification. It does not produce new durable-write, Kiban, or LMDB performance numbers.

To collect a native diagnostic (run separately from uninstrumented performance trials):

```sh
go test -c -o /tmp/golsm-writepath.test
taskset -c 1 env GOMAXPROCS=1 /tmp/golsm-writepath.test \
  -test.run='^$' -test.bench=BenchmarkMaintenanceWritePath -test.benchtime=100000x \
  -test.cpuprofile=/tmp/write.cpu -test.blockprofile=/tmp/write.block \
  -test.mutexprofile=/tmp/write.mutex
```
