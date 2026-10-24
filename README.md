# GoLSM

A persistent key-value store in Go, built from scratch to explore LSM storage engines. Recent writes live in a sharded, slice-chained hash table; older data lives in immutable sorted files. It has a WAL, persistent deletes, binary records with checksums, resident sparse indexes, Bloom filters, mmap reads, an LRU cache, range scans, and background compaction.

This is an educational engine with tests and reproducible benchmarks, rather than a production database. The [original design write-up](https://stratum.hashnode.dev/stratum-a-kv-store-in-go) describes the earlier implementation.

## Run it

Requires **Go 1.25+ on Linux or macOS**. There are no third-party dependencies. Run the entire package with `go run .`; the engine now spans several files.

```sh
go run . -dir ./data -sync-writes
```

The CLI accepts one command per line:

```text
PUT apple red
PUT banana yellow
GET apple
DELETE apple
FLUSH
GET apple
SCAN - - 20
COMPACT
STATS
EXIT
```

`GET apple` first prints `red`, then `key not found` after deletion. `SCAN` prints a JSON array of live entries in sorted order. `STATS` prints JSON metrics. EOF also closes the store. This command interface replaces the old numeric menu.

| Command | Behavior |
| --- | --- |
| `PUT key value` | Insert or replace; the value can contain spaces |
| `GET key` | Print the latest value, or `key not found` |
| `DELETE key` | Write a tombstone, including for keys found only on disk |
| `SCAN start end [limit]` | Sorted interval `[start, end)`; `-` is an unbounded endpoint; omit the limit or use `0` for all results |
| `FLUSH` | Wait until writes present at invocation have reached synced SSTables |
| `COMPACT` | Flush pending writes and merge all active SSTables |
| `STATS` | Inspect counters, memory/disk gauges, and background errors |
| `EXIT` / `QUIT` | Sync and close the WAL, stop the worker, release mappings and the directory lock |

The line-based CLI uses single-token keys and trims outer whitespace. The underlying Go methods support empty strings, whitespace, NULs, and arbitrary bytes in both keys and values. Keys sort by Go's bytewise string order. Records are limited to **64 MiB of key plus value**. The CLI scanner has its standard 64 KiB token limit; use the engine methods for larger records.

## Configuration and engine methods

```sh
go run . -dir ./data \
  -sync-writes \
  -flush-threshold=1000 \
  -cache-bytes=16777216 \
  -compaction-interval=30s \
  -compaction-threshold=4
```

| Setting | Default | Meaning |
| --- | --- | --- |
| `SyncWrites` / `-sync-writes` | `false` | `fsync` the WAL for each acknowledged mutation when enabled |
| `FlushThreshold` / `-flush-threshold` | `1000` | Rotate the WAL/memtable after this many operations and queue a background flush; apply writer backpressure if a frozen table is still pending |
| `CacheBytes` / `-cache-bytes` | `16 MiB` | Estimated LRU budget; `-1` disables the cache |
| `CompactionInterval` / `-compaction-interval` | `30s` | How often the worker checks for automatic compaction, in addition to flush-triggered checks; a negative duration disables compaction checks, while background flushing remains enabled |
| `CompactionThreshold` / `-compaction-threshold` | `4` | Number of newest, similarly sized SSTables in an automatic compaction job; must be at least `2` |

Zero-valued numeric/duration fields in `Options` select defaults. For example, `Options{CompactionInterval: -1, CacheBytes: -1}` disables automatic compaction and caching. At the CLI use `-compaction-interval=-1s`.

The engine is currently in `package main`, alongside the CLI. Its testable methods are:

```go
db, err := Open("./data", Options{SyncWrites: true})
// Handle err, then defer db.Close().
err = db.Put("name", "Keshav")
value, found, err := db.Get("name")
err = db.Delete("name")
rows, err := db.Scan("a", "z", 100) // a inclusive, z exclusive
err = db.Flush()
err = db.Compact()
metrics := db.Stats()
```

These are engine entry points for code in this package, not an independently importable library API. `Get` distinguishes an empty value (`found == true`) from a missing/deleted key (`found == false`). A zero scan limit is unlimited, and an empty `end` is unbounded. Methods reject operations after close with `ErrClosed`; `Close` is idempotent.

## How storage works

```text
PUT / DELETE
    │
    ├── lock the key's shard
    ├── append checked binary record to WAL
    ├── optionally fsync WAL
    └── update that shard's slice-chained hash table
                         │
                  flush threshold
                         │
           sync/rotate WAL, freeze memtable
                         │
          new writes use a new WAL + memtable
                         │
       background sort/write/sync SSTable + index
                         │
        publish MANIFEST + flushed WAL generation
                         │
              remove obsolete WAL segments

GET
    active memtable → frozen memtable → LRU → SSTables newest first
                         │
                key bounds → Bloom filter
                         │
                resident sparse index
                         │
                decode mapped records
```

- **Memtable:** 256 shards, each with its own `RWMutex`. Each shard uses FNV-1a hashing and slices for separate chaining, with local resizing above a 0.75 load factor. A directory-wide lock excludes a second process. The WAL append mutex preserves record integrity; operations on the same key retain WAL/memtable ordering. WAL appends remain serialized.
- **Tombstones:** deletion is a record flag, not a special user value. `<DELETED>` is a normal value. A tombstone stops older versions from being returned, including after a flush or restart.
- **Sparse indexes:** one key/offset checkpoint per 10 records, retained in memory. Binary search chooses a checkpoint; a short forward scan resolves the lookup. Key bounds skip unrelated files.
- **Bloom filters:** approximately 10 bits per key and seven probes. Filters include tombstones. A negative answer skips the table; a possible match still checks the actual records.
- **mmap:** immutable SSTable `.log` and `.index` files are mapped read-only. Point reads compare mapped key bytes and materialize only the matching value; bounds and checksums are validated on every traversed record. The mutable WAL uses ordinary append I/O.
- **LRU:** caches successful disk reads. Mutations invalidate their keys; flush and compaction retain unaffected entries. Cache fills are ordered against writes to the same shard to prevent stale refills. Its budget counts key/value bytes plus an estimated 64 bytes per entry; it is not a bound on total process RSS.
- **Compaction:** a streaming heap merge resolves duplicate keys newest-first. Automatic jobs merge a suffix of `CompactionThreshold` tables only when their largest/smallest size ratio is at most four. Partial jobs retain tombstones. Explicit `Compact` flushes pending writes, merges every active table, and can drop tombstones safely. A synced manifest publishes the replacement before old files are removed.
- **Ranges:** reuse sorted active/frozen memtable views until mutation, merge them with the newest disk versions, hide tombstones, and skip tables outside the requested interval.

Flush and compaction build SSTables outside the global lock; the exclusive lock protects WAL rotation and manifest publication. Maintenance jobs are serialized, keeping their input mappings alive. Readers hold the read lock until they finish, so publication waits for them before old mappings are reclaimed. Scans still hold the exclusive lock for their consistent merge. At most one frozen memtable is queued; if the replacement fills before its flush finishes, writers wait while readers can continue.

## Files, recovery, and durability

```text
data/
├── LOCK                  # advisory exclusive process lock
├── MANIFEST              # JSON list of the active SSTable IDs
├── data.log              # active write-ahead log
├── wal_00000000000000000001.log # frozen/recovered WAL segment, until checkpointed
├── sstable_1.log          # sorted records; binary despite the .log suffix
├── sstable_1.index        # sparse binary key/offset records
└── ...
```

Each new binary record contains:

```text
magic "KVR1" (4) | deletion flag (1) | key length (4) | value length (4)
CRC32C (4) | key bytes | value bytes
```

Lengths and checksums use little-endian encoding. CRC32C covers the flag, lengths, and payload. Index records use the same envelope, with an eight-byte offset as the value.

On open, the engine validates active SSTables, rebuilds their indexes and Bloom filters once, maps the files, and replays uncheckpointed WAL generations in order, followed by the active `data.log`. **Startup scans all active SSTable records**; metadata is not yet loaded from a persistent Bloom-filter format. A final incomplete WAL record is truncated. Malformed complete records and checksum failures produce errors. Legacy text `PUT key value` and `DELETE key` records remain readable, including in a WAL followed by newer binary records. A legacy directory gains a manifest automatically; future flushes and compactions write binary data. Old deletions that the earlier implementation already lost cannot be reconstructed.

WAL rotation syncs and renames the old WAL, creates and syncs a replacement, then switches writes to a new memtable. Background flush syncs its SSTable and index, then atomically publishes the table and flushed WAL generation in the manifest. Only afterward are checkpointed WAL segments removed. Recovery ignores segments at or below that checkpoint, preventing obsolete replay after a newer table has been published. Existing legacy directories open automatically; directories written by this revision require the WAL-generation-aware engine for recovery. Files absent from an existing manifest are ignored and cleaned up on open; missing referenced files are an error. Compaction uses the same publication boundary so leftover old files cannot resurrect deleted keys.

**Durability mode matters:**

- With the default `SyncWrites: false`, each mutation reaches the OS through the WAL before memory changes, but power loss can lose acknowledged writes that have not been synced. Flush and close sync their persistent output.
- With `SyncWrites: true`, each mutation waits for WAL `fsync` before it updates memory and returns. This has substantially higher latency on the benchmark host.
- WAL or manifest-publication failures stop further writes until reopen. A mutation can have reached the WAL even when its subsequent sync or automatic flush returns an error; callers should treat such errors as an uncertain outcome and inspect/reopen the database.

Keep database files under the engine's exclusive ownership. External modification or truncation of mapped files is unsupported.

## Metrics

`STATS` / `Stats()` expose process-local counters for writes, deletes, gets/misses, memtable/cache hits, Bloom checks/skips, table checks/reads, decoded records/bytes, WAL bytes, flushes, compactions, scans, and elapsed maintenance/recovery/startup time in nanoseconds.

Gauges include active SSTables, memtable entries, pending WAL operations, estimated cache bytes/budget, mapped bytes, Bloom bytes, sparse-index entries, and active database disk bytes. Background compaction and write errors are exposed as strings. `LogicalReadBytes`, `RecordsRead`, and table counters describe **point lookups**; they do not measure physical disk traffic or include scan/compaction/startup reads. Mapped size is virtual mapped bytes, not resident memory. Disk bytes cover the WAL, manifest, and active SSTable/index files.

## Validation

```sh
go vet ./...
go test -race -count=1 ./...
```

Tests cover deletes across flush/restart, literal tombstone-like values, binary and legacy records, sparse boundaries, corrupt and incomplete records, exclusive opens, cache eviction/invalidation, concurrent writes, scans, background compaction, unpublished/missing tables, publication failures, WAL replay after a published flush, a randomized model with repeated reopen/compaction, foreground progress during paused flush/compaction builds, bounded-queue backpressure, partial-compaction tombstones, and subprocess exits at WAL-rotation/publication boundaries. Both Linux and macOS are exercised. These are functional and race checks; they do not constitute a hardware power-failure certification.

## Benchmarks

The current optimization series adds cached scan views, allocation-light mapped lookups, selective cache retention, background flushes, unlocked compaction builds, and size-tier checks. Its before/after results are recorded separately under [`benchmarks/optimizations/`](benchmarks/optimizations/); the Kiban/LMDB comparison below describes the earlier pinned engine.

In the new **fast-mode 1M before/after comparison**, point-read p50 fell **13.31 → 3.78 µs**, verified point-read throughput rose **2.53×**, and latest-10 throughput rose **6.46×** with much smaller scan stalls. Scan p50 became slower, and fast insert/overwrite throughput fell **39%/45%**. These changes improve the large read workload with write and storage tradeoffs; see the [full results](benchmarks/optimizations/2026-10-24/matched-fast/REPORT.md). All 12 trials passed value, reopen, and recorded load checks. No new durable-write or Kiban/LMDB performance numbers are claimed.

### Matched Kiban and LMDB workload

The [engine comparison](benchmarks/engine-h2h/README.md) reruns the workload behind Kiban's post against **GoLSM, the post's pinned Kiban version, and raw LMDB**, in-process on pancakes. It uses the same Rust generator, JSON codec, timers, and value oracle, with GoLSM embedded through a Go C archive. Bridge calls and copies are included. The 48-trial campaign covers 10k and 1M base records, durable and fast modes, and passed all value/reopen checks across 8.88 million timed calls.

At 1M, GoLSM's durable insert/overwrite p50 is **1.36–1.38× faster than Kiban** and **1.21–1.35× faster than LMDB**, but verified write throughput is roughly tied with Kiban. LMDB leads point reads and latest-10 scans; GoLSM's 1M point-read p50 is **2.14× slower in durable mode and 7.48× slower in fast mode**. GoLSM's scans need work across both sizes.

Treat these as preliminary: some 10k trials exceeded the load gate, and the 1M groups have three repetitions on a shared VM. Fast-write p50 hides flush/compaction costs, and the engines have different maintenance policies. See the [full tables, tails, throughput, and resource measurements](benchmarks/engine-h2h/results/2026-10-24/REPORT.md), [interactive comparison](benchmarks/engine-h2h/results/2026-10-24/comparison.html), and [reproduction instructions](benchmarks/engine-h2h/README.md#reproduce).

### Native Go microbenchmarks

These earlier raw-string tests use a different workload from the JSON agent-memory comparison above; their numbers should not be compared directly.

Measured on **pancakes**, Linux/amd64, Go 1.25.1, with 2 vCPUs from an AMD EPYC 7763 and approximately 8 GiB RAM. The run began **October 23, 2026 at 20:36 UTC** (October 24 in India). Engine revision: `03b42cf`. Values below are medians of three samples, not best-case results.

| Microbenchmark | Median time/op | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| PUT, buffered WAL, including threshold flushes | 33.66 µs | 873 | 8 |
| PUT, per-write WAL fsync | 4.87 ms | 194 | 2 |
| GET, memtable | 130.6 ns | 0 | 0 |
| GET, mapped SSTable, LRU disabled | 1.69 µs | 704 | 11 |
| GET, prewarmed LRU | 190.7 ns | 0 | 0 |
| GET, absent key with Bloom filter | 185.0 ns | 6 | 0 |
| GET, absent key with Bloom disabled | 927.0 ns | 831 | 12 |
| GET, parallel memtable throughput | 79.3 ns | 0 | 0 |
| Parallel mix: 50% read / 50% write | 25.03 µs | 863 | 10 |
| Parallel mix: 95% read / 5% write | 2.83 µs | 434 | 6 |
| SCAN, return 100 keys | 43.09 µs | 32,737 | 337 |
| FLUSH, 1,000 records | 27.63 ms | 627,475 | 5,464 |
| COMPACT, four versions of 1,000 keys | 29.77 ms | 989,086 | 13,216 |
| OPEN + CLOSE, replay 20,000 WAL records | 14.71 ms | 9,997,136 | 126,425 |

Parallel `ns/op` describes aggregate throughput across Go benchmark workers, not individual request latency. Per-write fsync ranged from **4.86 to 13.27 ms/op** across samples; its one-second samples performed fewer than 1,000 operations, so they did not trigger a threshold flush. Recovery here measures a WAL-only database, not startup over a large collection of SSTables. Flush/compaction timing excludes seeding the data.

The separate, **single-worker buffered-WAL workload** produced:

| Read / write mix | Median ops/s | Read p50 | Read p95 | Read p99 | Write p99 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 50 / 50 | 58,996 | 1.83 µs | 4.55 µs | 9.36 µs | 10.18 µs |
| 95 / 5 | 357,251 | 972.0 ns | 2.77 µs | 6.05 µs | 14.26 µs |
| 100 / 0 | 1,092,840 | 270.0 ns | 2.27 µs | 5.33 µs | — |

These columns are medians of per-trial values, not percentiles pooled across trials. Threshold flushes occur on roughly 0.1% of writes, so p99 does **not** capture the full flush tail even though throughput includes those pauses. In the 50/50 workload, compaction reduced the median active disk footprint from **6.09 MB to 2.74 MB** (about **55%**) and took a median **92.1 ms** for 20,000 live keys. Full race checks passed on Linux and macOS.

Reproduce the measurements from the repository root:

```sh
./benchmarks/run.sh
```

The script runs `go vet`, the full race suite, three one-second samples of each point-operation benchmark, three five-iteration samples of flush/compaction/recovery, and three trials of each mixed workload. Raw results and hardware details are in [`benchmarks/`](benchmarks/). Benchmark data uses temporary directories.

The mixed workload runner performs 50,000 operations against 20,000 initially populated keys per trial, with uniform key selection, 16-byte keys, and 100-byte values. Writes update existing keys. It records throughput and p50/p95/p99 read/write latency, then measures compaction and validates every key after reopen. Automatic compaction is disabled during measurements; the recorded historical buffered and mixed write benchmarks include synchronous threshold flushes. Running the script on the current engine instead includes WAL rotations, background flushing, and writer backpressure. The in-process ratios resemble common YCSB read/update mixes, but this is **not a YCSB or memtier run**. There is no network protocol for memtier to target.

Read data fits in RAM, and mapped-read tests have warm OS page caches. The cached benchmark prewarms the LRU; the SSTable and missing-key benchmarks disable it. The missing-key comparison changes only the Bloom filter. These numbers do not describe cold-device reads, large datasets under memory pressure, or remote-client latency. Microbenchmark `ns/op` is an average; workload latency percentiles come from per-operation wall-clock samples and include instrumentation overhead. No speedup over the original implementation is claimed without a comparable baseline.

## Remaining limits

- Single process per directory; Linux/macOS only. No network API, transactions, replication, or multi-key atomic writes.
- Scans, WAL rotation, and manifest publication still require the exclusive lock. Flush/compaction construction runs outside it; storage syncs at publication can still cause pauses. Scans retain sorted memtable snapshots and materialize returned results.
- The memtable threshold counts operations, not bytes. The frozen queue has writer backpressure, but there is no total byte budget; large records can still consume substantial memory.
- Automatic compaction uses similarly sized suffixes, not a complete leveled layout. Each selected job still rewrites whole input files; there is no compression or fixed byte/time limit per job.
- Startup validates all SSTables and rewrites sparse indexes. Bloom filters are rebuilt rather than persisted.
- WAL records have checksums, but there are no sequence-numbered batches, transactions, or fault-injection coverage of every filesystem failure boundary.
