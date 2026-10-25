# GoLSM optimization results: alternating revisions

Baseline `5d13cf58dc2f80a9ad579ecf57e67b1ec5ebf3d9` → optimized `37abd158624d6a0a629c10105ef02e0c31f46425`. Fast mode only, three repetitions per size and revision, same upstream W1–W5 workload on pancakes. No new Kiban/LMDB or durable performance measurements.

Implementation changes, interpretation, and reproduction are in the [parent benchmark README](../../README.md).

Audited **12 trials**, **3,300,000 timed calls**, **1,386,000 write read-backs**, and **6,720,000 records after new-process reopen**; no value errors.

All trials retained. Some trials failed the recorded load/coverage gate. These remain preliminary shared-VM measurements with three repetitions. Tables use medians of per-trial statistics; ratios are descriptive, not confidence intervals. Go bridge/copies remain inside call timing.

## Base N=10,000

| Workload | Before p50 µs | After p50 µs | p50 speedup | Before verified ops/s | After verified ops/s | Throughput ratio |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| remember | 2.816 | 2.880 | 0.98× | 16,297 | 20,024 | 1.23× |
| recall-by-id | 2.112 | 1.664 | 1.27× | 141,661 | 168,909 | 1.19× |
| recent-for-agent | 18.944 | 19.968 | 0.95× | 29,906 | 26,055 | 0.87× |
| update | 3.008 | 3.136 | 0.96× | 13,909 | 16,583 | 1.19× |
| mixed-90-10 | 2.368 | 2.304 | 1.03× | 79,704 | 96,494 | 1.21× |

| Workload | Revision | p50 trial range µs | p99 µs | p999 µs | Median trial max µs | Largest observed µs |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| remember | baseline | 2.816–3.008 | 22.016 | 6291.456 | 94502.117 | 95693.796 |
| remember | optimized | 2.816–2.880 | 25.600 | 2228.224 | 63030.492 | 64077.841 |
| recall-by-id | baseline | 1.600–2.560 | 17.408 | 135.168 | 15119.214 | 15286.097 |
| recall-by-id | optimized | 1.504–2.240 | 11.264 | 139.264 | 10186.900 | 16479.377 |
| recent-for-agent | baseline | 18.432–22.016 | 159.744 | 217.088 | 1518.540 | 3167.965 |
| recent-for-agent | optimized | 18.944–33.792 | 163.840 | 835.584 | 1438.830 | 3195.125 |
| update | baseline | 3.008–3.072 | 20.992 | 6815.744 | 204591.473 | 218660.943 |
| update | optimized | 3.008–3.648 | 25.088 | 417.792 | 152330.090 | 162782.165 |
| mixed-90-10 | baseline | 2.240–2.560 | 19.968 | 176.128 | 17466.394 | 21833.941 |
| mixed-90-10 | optimized | 2.240–3.456 | 17.920 | 159.744 | 12605.193 | 20206.749 |
## Base N=1,000,000

| Workload | Before p50 µs | After p50 µs | p50 speedup | Before verified ops/s | After verified ops/s | Throughput ratio |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| remember | 3.328 | 3.840 | 0.87× | 12,884 | 15,301 | 1.19× |
| recall-by-id | 4.032 | 4.096 | 0.98× | 133,931 | 136,456 | 1.02× |
| recent-for-agent | 26.112 | 25.600 | 1.02× | 20,005 | 20,705 | 1.04× |
| update | 3.392 | 3.392 | 1.00× | 11,298 | 13,820 | 1.22× |
| mixed-90-10 | 4.608 | 4.864 | 0.95× | 69,400 | 71,040 | 1.02× |

| Workload | Revision | p50 trial range µs | p99 µs | p999 µs | Median trial max µs | Largest observed µs |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| remember | baseline | 3.328–3.392 | 32.768 | 12845.056 | 587508.171 | 1696170.902 |
| remember | optimized | 3.456–4.608 | 36.864 | 15204.352 | 473427.633 | 1641367.512 |
| recall-by-id | baseline | 3.968–4.224 | 20.480 | 90.112 | 15453.189 | 15693.428 |
| recall-by-id | optimized | 4.096–6.016 | 18.944 | 53.248 | 15294.353 | 26604.323 |
| recent-for-agent | baseline | 25.088–32.768 | 208.896 | 1835.008 | 9617.135 | 22557.955 |
| recent-for-agent | optimized | 24.576–38.912 | 208.896 | 1736.704 | 12310.449 | 109007.830 |
| update | baseline | 3.264–3.520 | 32.256 | 19922.944 | 887718.606 | 1041515.534 |
| update | optimized | 3.392–4.608 | 31.232 | 15466.496 | 832814.126 | 7339647.089 |
| mixed-90-10 | baseline | 4.608–4.992 | 27.136 | 114.688 | 68045.621 | 96704.689 |
| mixed-90-10 | optimized | 4.736–6.656 | 29.184 | 229.376 | 60014.122 | 77967.870 |

## Maintenance, final drain, and resources

Final sync/flush/full verification happens outside the foreground timer. The combined finish duration includes verification; it is not isolated maintenance CPU time. RSS is whole-process memory including the oracle/runtimes; peak RSS includes preload.

| Base | Revision | Trial | Load median | Gate | Flushes | Compactions | Compaction seconds | Finish + verification seconds | Peak RSS MiB | Closed disk MiB |
| ---: | --- | ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 10,000 | baseline | 1 | 0.24 | pass | 21 | 6 | 0.370 | 0.106 | 36.9 | 9.5 |
| 10,000 | baseline | 2 | 0.59 | pass | 21 | 6 | 0.351 | 0.098 | 37.2 | 9.5 |
| 10,000 | baseline | 3 | 0.59 | pass | 21 | 6 | 0.367 | 0.091 | 37.6 | 9.5 |
| 1,000,000 | baseline | 1 | 1.38 | pass | 210 | 68 | 6.077 | 6.702 | 1450.8 | 412.0 |
| 1,000,000 | baseline | 2 | 4.88 | preliminary | 210 | 68 | 5.613 | 6.720 | 1407.6 | 412.0 |
| 1,000,000 | baseline | 3 | 3.77 | preliminary | 210 | 68 | 5.816 | 6.211 | 1404.8 | 412.0 |
| 10,000 | optimized | 1 | 0.46 | pass | 21 | 6 | 0.351 | 0.145 | 34.7 | 9.5 |
| 10,000 | optimized | 2 | 0.46 | pass | 21 | 6 | 0.327 | 0.088 | 36.8 | 9.5 |
| 10,000 | optimized | 3 | 0.70 | pass | 21 | 6 | 0.332 | 0.122 | 40.7 | 9.5 |
| 1,000,000 | optimized | 1 | 7.62 | preliminary | 210 | 68 | 12.541 | 11.074 | 1123.5 | 412.0 |
| 1,000,000 | optimized | 2 | 7.96 | preliminary | 210 | 68 | 4.640 | 6.557 | 1166.1 | 412.0 |
| 1,000,000 | optimized | 3 | 3.24 | preliminary | 210 | 68 | 5.152 | 6.256 | 1404.2 | 412.0 |

The optimized engine acknowledges threshold writes after WAL rotation and queues one immutable flush. It applies writer backpressure when the next memtable fills. Compaction construction no longer holds the global lock, and automatic jobs select similarly sized suffixes instead of rewriting the whole base. Those changes affect storage layout and where work occurs. Throughput includes generation/oracle/write read-backs, but excludes preload and final finish; this is not maintenance-drained saturation capacity.
