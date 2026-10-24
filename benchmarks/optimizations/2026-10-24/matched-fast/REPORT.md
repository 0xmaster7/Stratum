# GoLSM optimization results: alternating revisions

Baseline `b9a1c07a85be14eabdcba6dc129bfeaf7a59a82a` → optimized `5d13cf58dc2f80a9ad579ecf57e67b1ec5ebf3d9`. Fast mode only, three repetitions per size and revision, same upstream W1–W5 workload on pancakes. No new Kiban/LMDB or durable performance measurements.

Audited **12 trials**, **3,300,000 timed calls**, **1,386,000 write read-backs**, and **6,720,000 records after new-process reopen**; no value errors.

All trials retained. Every trial passed the recorded load/coverage gate. These remain preliminary shared-VM measurements with three repetitions. Tables use medians of per-trial statistics; ratios are descriptive, not confidence intervals. Go bridge/copies remain inside call timing.

## Base N=10,000

| Workload | Before p50 µs | After p50 µs | p50 speedup | Before verified ops/s | After verified ops/s | Throughput ratio |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| remember | 2.624 | 2.752 | 0.95× | 24,357 | 17,590 | 0.72× |
| recall-by-id | 2.880 | 1.472 | 1.96× | 194,341 | 186,039 | 0.96× |
| recent-for-agent | 37.888 | 18.432 | 2.06× | 17,015 | 30,592 | 1.80× |
| update | 2.688 | 3.200 | 0.84× | 24,724 | 14,676 | 0.59× |
| mixed-90-10 | 3.328 | 2.368 | 1.41× | 105,885 | 74,026 | 0.70× |

| Workload | Revision | p50 trial range µs | p99 µs | p999 µs | Median trial max µs | Largest observed µs |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| remember | baseline | 2.624–2.688 | 22.016 | 112.640 | 39876.591 | 41430.567 |
| remember | optimized | 2.752–2.752 | 22.016 | 6422.528 | 95099.733 | 106049.512 |
| recall-by-id | baseline | 2.816–2.944 | 18.944 | 129.024 | 310.460 | 535.531 |
| recall-by-id | optimized | 1.408–1.536 | 14.080 | 116.736 | 10507.412 | 13597.381 |
| recent-for-agent | baseline | 37.888–38.912 | 204.800 | 344.064 | 1019.486 | 2185.958 |
| recent-for-agent | optimized | 18.432–18.944 | 151.552 | 212.992 | 1427.991 | 1592.288 |
| update | baseline | 2.624–2.816 | 21.504 | 196.608 | 36735.235 | 37160.300 |
| update | optimized | 3.008–3.264 | 26.624 | 9175.040 | 189707.344 | 193569.916 |
| mixed-90-10 | baseline | 3.328–3.456 | 21.504 | 124.928 | 34745.884 | 37484.006 |
| mixed-90-10 | optimized | 2.176–2.688 | 20.992 | 720.896 | 16756.470 | 43240.873 |
## Base N=1,000,000

| Workload | Before p50 µs | After p50 µs | p50 speedup | Before verified ops/s | After verified ops/s | Throughput ratio |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| remember | 2.688 | 2.944 | 0.91× | 25,258 | 15,287 | 0.61× |
| recall-by-id | 13.312 | 3.776 | 3.53× | 59,564 | 150,775 | 2.53× |
| recent-for-agent | 15.616 | 22.528 | 0.69× | 3,804 | 24,566 | 6.46× |
| update | 2.752 | 3.072 | 0.90× | 25,073 | 13,849 | 0.55× |
| mixed-90-10 | 13.824 | 4.480 | 3.09× | 47,679 | 75,570 | 1.58× |

| Workload | Revision | p50 trial range µs | p99 µs | p999 µs | Median trial max µs | Largest observed µs |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| remember | baseline | 2.688–2.688 | 23.040 | 2228.224 | 79432.130 | 252101.892 |
| remember | optimized | 2.944–3.008 | 23.552 | 11010.048 | 447172.903 | 479347.740 |
| recall-by-id | baseline | 13.312–13.568 | 37.888 | 212.992 | 6787.224 | 8656.810 |
| recall-by-id | optimized | 3.712–3.904 | 16.128 | 114.688 | 15249.921 | 23387.822 |
| recent-for-agent | baseline | 15.616–16.128 | 1376.256 | 4587.520 | 5303966.251 | 5309113.851 |
| recent-for-agent | optimized | 22.528–23.040 | 159.744 | 999.424 | 12125.509 | 23588.969 |
| update | baseline | 2.688–2.752 | 24.576 | 4194.304 | 82658.585 | 90507.854 |
| update | optimized | 3.008–3.072 | 23.552 | 15466.496 | 744426.614 | 789603.925 |
| mixed-90-10 | baseline | 13.568–15.360 | 49.152 | 229.376 | 40587.420 | 47533.090 |
| mixed-90-10 | optimized | 4.352–4.480 | 22.528 | 208.896 | 46565.952 | 62575.822 |

## Maintenance, final drain, and resources

Final sync/flush/full verification happens outside the foreground timer. The combined finish duration includes verification; it is not isolated maintenance CPU time. RSS is whole-process memory including the oracle/runtimes; peak RSS includes preload.

| Base | Revision | Trial | Load median | Gate | Flushes | Compactions | Compaction seconds | Finish + verification seconds | Peak RSS MiB | Closed disk MiB |
| ---: | --- | ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 10,000 | baseline | 1 | 1.50 | pass | 21 | 0 | 0.000 | 0.116 | 42.2 | 10.4 |
| 10,000 | baseline | 2 | 1.44 | pass | 21 | 0 | 0.000 | 0.124 | 42.4 | 10.4 |
| 10,000 | baseline | 3 | 1.43 | pass | 21 | 0 | 0.000 | 0.117 | 44.1 | 10.4 |
| 1,000,000 | baseline | 1 | 1.39 | pass | 210 | 2 | 10.841 | 22.767 | 1238.3 | 375.7 |
| 1,000,000 | baseline | 2 | 1.43 | pass | 210 | 2 | 10.840 | 23.030 | 1359.0 | 375.7 |
| 1,000,000 | baseline | 3 | 2.54 | pass | 210 | 2 | 10.910 | 23.232 | 1318.0 | 375.7 |
| 10,000 | optimized | 1 | 1.46 | pass | 21 | 6 | 0.395 | 0.098 | 38.6 | 9.5 |
| 10,000 | optimized | 2 | 1.46 | pass | 21 | 6 | 0.363 | 0.094 | 38.8 | 9.5 |
| 10,000 | optimized | 3 | 1.41 | pass | 21 | 6 | 0.358 | 0.136 | 53.8 | 9.5 |
| 1,000,000 | optimized | 1 | 1.59 | pass | 210 | 68 | 4.803 | 5.453 | 1443.3 | 412.0 |
| 1,000,000 | optimized | 2 | 1.40 | pass | 210 | 68 | 4.725 | 5.416 | 1263.9 | 412.0 |
| 1,000,000 | optimized | 3 | 1.85 | pass | 210 | 68 | 4.857 | 5.437 | 1501.4 | 412.0 |

The optimized engine acknowledges threshold writes after WAL rotation and queues one immutable flush. It applies writer backpressure when the next memtable fills. Compaction construction no longer holds the global lock, and automatic jobs select similarly sized suffixes instead of rewriting the whole base. Those changes affect storage layout and where work occurs. Throughput includes generation/oracle/write read-backs, but excludes preload and final finish; this is not maintenance-drained saturation capacity.
