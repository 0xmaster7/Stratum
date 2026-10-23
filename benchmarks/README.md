# Recorded benchmark run

These artifacts were generated on `pancakes` by `./benchmarks/run.sh` at engine commit `03b42cf`. See the repository README for result tables, configuration, and interpretation. The machine was running other services; CPU scheduling and storage latency can vary.

| File | Contents |
| --- | --- |
| `environment.txt` | UTC timestamp, Go version, kernel, CPU topology, commit, and initial host load |
| `correctness.txt` | Full Linux `go test -race -count=1 ./...` result; `go vet ./...` also succeeded before it |
| `micro.txt` | Three samples per point-operation, parallel, mixed, and scan benchmark; one second per sample |
| `maintenance.txt` | Three samples per flush, compaction, and WAL-recovery benchmark; five iterations per sample |
| `workload.json` | Three trials each of uniform 50/50, 95/5, and 100/0 read/update workloads, with latency, compaction, and storage metrics |
| `workload-run.txt` | Workload runner result, including post-compaction/reopen validation |

Run from the repository root:

```sh
./benchmarks/run.sh
```

This overwrites the recorded files. Go must be on `PATH`. The suite uses temporary database directories; it does not modify a user database.

The microbenchmarks use 10,000 keys (16 bytes) and 100-byte values, except the dedicated maintenance cases. Point reads are sequential over those keys. `GetParallel` uses concurrent Go workers. Mixed microbenchmarks use concurrent workers and seeded xorshift key selection; the separate JSON workload uses a single worker, uniform pseudo-random keys, 20,000 initial keys, and 50,000 measured operations. Each JSON workload trial starts with a newly seeded database and an empty read cache. All measurements disable the automatic compactor, so explicit maintenance costs and threshold flushes remain visible.

`Put/Buffered` includes threshold flushes at 1,000 WAL operations. `Put/Sync` waits for fsync on each operation and is much slower; in this run, each sample finished before reaching the flush threshold. `Get/Memtable` has no flushed tables; the other `Get` modes use one flushed table. `Get/Cached` prewarms the read cache. `Get/SSTable`, `MissingBloom`, and `MissingNoBloom` disable the LRU. The two missing-key cases probe interleaved absent keys inside the table's key bounds; only Bloom filtering changes between them. A nil Bloom filter is used by the benchmark to force every query through the index/record path.

`Flush1000` times flushing 1,000 pending records. `Compaction` merges four tables containing the same 1,000 keys. Data preparation is excluded from those timings. `Recovery20000` times opening and closing a database whose WAL contains 20,000 records and whose SSTable set is empty. Its timing includes replay, lock acquisition, and close, rather than just a decoding loop.

Values in the README are medians of the three reported samples. Go's integer allocation statistics are rounded per operation; `0 allocs/op` for Bloom-filtered misses still includes occasional false-positive allocations, visible in `B/op`. Warm mapped reads use the OS page cache. No cache dropping, large-data memory pressure, network clients, YCSB driver, or memtier driver is involved.
