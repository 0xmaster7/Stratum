# GoLSM optimization follow-up

This series addresses the scan/read/maintenance weaknesses exposed by the original [Kiban/LMDB comparison](../engine-h2h/README.md). Each engine change was committed separately:

| Commit | Change |
| --- | --- |
| `2092c70` | Reuse sorted memtable views between mutations; skip nonoverlapping range tables |
| `06a1b3a` | Compare mapped keys without allocating skipped keys/values; preserve checksum checks and owned return values |
| `6257169` | Preserve valid read-cache entries across maintenance; serialize cache fills against same-shard mutations |
| `4a0c91c` | Build compaction output outside the global lock; serialize maintenance and drain readers before reclaiming mappings |
| `8c63784` | Rotate WAL generations, flush one immutable memtable in the background, bound the queue with writer backpressure, and checkpoint replay in the manifest |
| `495f15a` | Compact a suffix of similarly sized tables automatically; retain partial-job tombstones and keep explicit full compaction |
| `5d13cf5` | Schedule compaction after each background flush and catch up through eligible size tiers |

The current engine revision measured by the follow-up is **`5d13cf5`**. The baseline is **`b9a1c07`**, the same engine revision used in the earlier three-engine campaign.

## Validation

`go vet ./...` and the full race suite passed on macOS and Linux. The release Rust driver passed all five tests, including all three engines in both durability modes, full-value/reopen checks, and deliberate corruption rejection. New tests check scan-view invalidation, skipped-record checksums, returned-value lifetime, cache retention/concurrent updates, foreground progress during deliberately paused flush/compaction construction, writer backpressure, partial-compaction tombstone safety, and subprocess exits around WAL rotation/publication. [Recorded validation outputs](2026-10-24/validation/) are included. These tests do not simulate hardware power loss.

## Measurements

The initial focused native samples are in [`2026-10-24/`](2026-10-24/). They are exploratory, unpinned samples on a noisy VM. Their allocation reductions are useful: mapped point reads fell from ten allocations/703 bytes to one allocation/112 bytes; repeated small scans with 900 pending entries fell from about 113 KB to 2.9 KB per call. The timing samples are retained without presenting their large observed ratio as a stable end-to-end speedup.

The matched follow-up alternates old/new GoLSM binaries on pancakes, reversing their order every repetition. It uses CPU 1 for the engine, CPU 0 for the observer, the same seed/64 agents/JSON model/latest-10 query as the original comparison, and three repetitions at each of 10k and 1M base records. It performs **fast mode only**: 10k calls per stage at 10k base, 100k at 1M, discarded warmup, untimed preload, full initial/reopen checks, immediate write read-backs, final sync/full verification, and new-process verification. Every trial is retained, including load-gate failures.

The follow-up compares engine revisions, not freshly measured Kiban/LMDB results. It does not establish a new durable-write speedup, cold-cache behavior, sustained saturation throughput, or an equal-maintenance comparison. Background work still consumes CPU/I/O; rotation and manifest publication retain sync costs, and scans still take an exclusive lock. Automatic jobs bound the number of input files and their relative sizes, not a fixed wall-clock or byte limit.

## Final results

The [complete report](2026-10-24/matched-fast/REPORT.md), [interactive comparison](2026-10-24/matched-fast/comparison.html), and [independent audit](2026-10-24/matched-fast/audit.json) cover **12 trials, 3.3M timed calls, 1.386M immediate write read-backs, and 6.72M records verified after new-process reopen**. All value checks, reopen checks, and recorded load gates passed. The final campaign ran 13:43–13:50 UTC on October 24, 2026.

At **1M base records**, medians across three trials:

- Point reads: p50 **13.312 → 3.776 µs** (3.53× lower); verified throughput **59.6k → 150.8k ops/s** (2.53× higher).
- Latest 10: verified throughput **3.8k → 24.6k ops/s** (6.46× higher), p99 **1.376 → 0.160 ms**, and largest observed call **5.309 s → 23.6 ms**. Its p50 worsened **15.616 → 22.528 µs**; removing large stalls matters more than the median here.
- 90/10 mix: p50 **13.824 → 4.480 µs** and verified throughput **47.7k → 75.6k ops/s** (1.58× higher).
- **Fast writes regressed:** verified insert throughput **25.3k → 15.3k ops/s** (39% lower) and overwrites **25.1k → 13.8k ops/s** (45% lower). The largest observed insert/overwrite calls also worsened to **479/790 ms**, from **252/91 ms**. Background maintenance does not remove its cost; WAL rotation, publication syncs, and writer backpressure remain optimization targets, requiring profiling before attributing the gap.
- Closed disk footprint increased **375.7 → 412.0 MiB** (about 10%) as smaller compaction jobs retained more versions. Whole-process peak RSS varied; no memory reduction is claimed.

At **10k**, point-read p50 nearly halved, but verified point-read throughput fell about 4%. Latest-10 p50 halved and throughput rose 1.80×. Inserts, overwrites, and mixed-workload throughput fell. The result is a substantial improvement to large-dataset reads and scan stalls, with real write and storage tradeoffs—not an across-the-board speedup.

The next performance target is to profile WAL-rotation/publication syncs and queue waiting, then reduce their cost while keeping the new crash-recovery guarantees.

## Scheduling experiment retained

The first alternating campaign, engine `495f15a`, was stopped after eight completed trials when the first 1M optimized scan exposed a scheduling regression. Its p50 rose from 23.55 µs in the paired baseline to 335.87 µs: small SSTables accumulated while automatic compaction ran only on its 30-second timer. Its raw outputs and stop marker are retained in [`2026-10-24/scheduler-experiment/`](2026-10-24/scheduler-experiment/), including the incomplete ninth trial. It is not presented as a completed comparison. The fix schedules and cascades eligible compactions after flushes; the final comparison starts fresh and retains every trial.

## Reproduction

Keep the original baseline executable before rebuilding. `compare_revisions.py` verifies its SHA-256 against the recorded original campaign and records the optimized executable's hash and source revision. On Linux, from the repository root:

```sh
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"
benchmarks/engine-h2h/build.sh
cp benchmarks/engine-h2h/target/release/golsm-engine-h2h /tmp/golsm-baseline.bin
(cd benchmarks/engine-h2h && python3 run.py --output work/baseline-provenance --sizes 10000 --small-runs 1 --engines golsm)
GOLSM_REF=5d13cf5 benchmarks/engine-h2h/build.sh
cd benchmarks/engine-h2h
cargo test --release --locked -- --test-threads=1
python3 compare_revisions.py --output work/fresh-followup --before-binary /tmp/golsm-baseline.bin \
  --baseline-metadata work/baseline-provenance/metadata.json
```

The original executable hash check is deliberately strict: rebuilding the baseline with different compilers or paths can change its bytes. For a new build, the command above records a separate verified baseline provenance campaign and passes its metadata explicitly. The checked-in historical metadata stays unchanged.
