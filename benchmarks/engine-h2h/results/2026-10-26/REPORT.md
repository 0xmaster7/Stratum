# GoLSM vs Kiban vs LMDB: matched engine workload

All engines ran in one release executable with in-process calls on pancakes. These are new measurements, not the post’s hardware results. GoLSM uses a Go C archive; its measured latency includes the foreign-function call and result copy. The original Kiban/LMDB adapters and the upstream generator, oracle, and histogram are retained.

Validated **48 trials**, **8,880,000 timed calls**, **2,790,000 immediate write read-backs**, and **19,590,000 records after new-process reopen**. No value errors.

Engine pins: GoLSM `bf0bc4d6cc88bccf9dd329cdc6a9164eb0d070b7`, Kiban `361305a8b63fef309e301e8106c254e60a70ac84`, LMDB `lmdb-master3-sys 0.2.5` through `heed3 0.22.0`.

The original direct Kiban handle buffers its WAL and defers explicit flushing to the excluded finish step. GoLSM automatically flushes at 1,000 writes; its flush and compaction implementation is determined by the recorded engine revision. Thus fast p50 is foreground acknowledgment latency with different buffering/maintenance policies, not equal sustained compaction-drained capacity. GoLSM also pays its measured C-archive bridge overhead.

This GoLSM revision queues frozen memtables with bounded writer backpressure, triggers bounded tier compaction after flushes, uses numbered active WALs, avoids directory syncs when deleting checkpointed files, and builds SSTable indexes/Bloom filters during streaming writes. Durable mutations still sync their WAL before acknowledgment.

Cells are medians of trial statistics, with trial ranges where shown. Latency histogram quantization is under 3.125%. No trials were dropped. These are shared-VM results; the 1M results remain preliminary with three repetitions. Full settings and limitations are in the benchmark README.

## Base N=10,000, durable

**Preliminary: one or more trials exceeded the retained load/coverage gate. All trials are included.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 4587.520 [4325.376, 6160.384] | 4325.376 [4325.376, 4325.376] | 5111.808 [4980.736, 5242.880] | 1.061× | 1.182× |
| recall-by-id | 1.152 [1.088, 1.568] | 1.696 [1.632, 3.328] | 1.280 [1.248, 2.176] | 0.679× | 0.755× |
| recent-for-agent | 4.864 [4.864, 5.120] | 19.968 [18.432, 39.936] | 3.776 [3.776, 7.552] | 0.244× | 0.189× |
| update | 4325.376 [4325.376, 4456.448] | 4325.376 [4325.376, 4456.448] | 4849.664 [4718.592, 4980.736] | 1.000× | 1.121× |
| mixed-90-10 | 2.048 [2.016, 3.520] | 3.392 [3.328, 3.648] | 2.432 [2.368, 2.944] | 0.604× | 0.717× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 20447.232 | 25690.112 | 229834.900 | 180 | 180 |
| golsm | remember | 22020.096 | 27787.264 | 178730.448 | 200 | 201 |
| lmdb | remember | 44040.192 | 50331.648 | 216100.105 | 97 | 97 |
| kiban | recall-by-id | 2.816 | 12.288 | 29.435 | 384,583 | 821,222 |
| golsm | recall-by-id | 15.616 | 135.168 | 9715.514 | 170,360 | 235,563 |
| lmdb | recall-by-id | 2.432 | 12.544 | 33.022 | 364,868 | 747,111 |
| kiban | recent-for-agent | 10.496 | 26.112 | 39.554 | 81,148 | 197,724 |
| golsm | recent-for-agent | 159.744 | 344.064 | 2035.848 | 27,686 | 35,899 |
| lmdb | recent-for-agent | 8.064 | 16.384 | 27.010 | 89,861 | 253,613 |
| kiban | update | 22020.096 | 26738.688 | 205503.510 | 190 | 191 |
| golsm | update | 22544.384 | 28311.552 | 240800.879 | 199 | 200 |
| lmdb | update | 44040.192 | 47185.920 | 185797.313 | 108 | 108 |
| kiban | mixed-90-10 | 6029.312 | 20971.520 | 29629.272 | 1,998 | 2,006 |
| golsm | mixed-90-10 | 6291.456 | 21495.808 | 26483.780 | 2,008 | 2,022 |
| lmdb | mixed-90-10 | 26738.688 | 42991.616 | 48447.352 | 1,070 | 1,073 |

## Base N=10,000, fast

**Preliminary: one or more trials exceeded the retained load/coverage gate. All trials are included.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 1.184 [1.152, 1.984] | 3.008 [2.944, 4.736] | 8.704 [8.704, 13.568] | 0.394× | 2.894× |
| recall-by-id | 0.992 [0.976, 1.696] | 1.696 [1.632, 2.944] | 1.216 [1.152, 2.112] | 0.585× | 0.717× |
| recent-for-agent | 4.864 [4.736, 9.216] | 19.968 [18.432, 35.840] | 3.776 [3.776, 7.552] | 0.244× | 0.189× |
| update | 1.600 [1.536, 2.560] | 3.328 [3.136, 5.120] | 8.960 [8.960, 14.080] | 0.481× | 2.692× |
| mixed-90-10 | 0.896 [0.832, 1.600] | 2.432 [2.176, 3.968] | 1.312 [1.088, 2.112] | 0.368× | 0.539× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 9.728 | 21.504 | 117.780 | 273,195 | 614,326 |
| golsm | remember | 27.648 | 2621.440 | 65136.573 | 20,412 | 22,162 |
| lmdb | remember | 23.040 | 34.816 | 109.204 | 79,362 | 99,250 |
| kiban | recall-by-id | 2.304 | 6.016 | 25.288 | 420,594 | 977,955 |
| golsm | recall-by-id | 16.384 | 41.984 | 9275.563 | 150,143 | 193,655 |
| lmdb | recall-by-id | 2.240 | 11.776 | 28.253 | 376,950 | 778,434 |
| kiban | recent-for-agent | 10.496 | 23.040 | 47.589 | 79,829 | 194,023 |
| golsm | recent-for-agent | 155.648 | 241.664 | 1630.289 | 28,758 | 37,157 |
| lmdb | recent-for-agent | 7.808 | 16.384 | 32.010 | 89,924 | 255,734 |
| kiban | update | 10.496 | 19.968 | 111.589 | 238,054 | 488,419 |
| golsm | update | 28.160 | 2097.152 | 140872.161 | 17,388 | 18,762 |
| lmdb | update | 17.920 | 28.160 | 42.960 | 84,400 | 107,786 |
| kiban | mixed-90-10 | 2.944 | 13.312 | 24.967 | 378,812 | 831,455 |
| golsm | mixed-90-10 | 18.944 | 155.648 | 15531.447 | 99,455 | 122,381 |
| lmdb | mixed-90-10 | 12.544 | 22.016 | 38.943 | 268,372 | 456,608 |

## Base N=1,000,000, durable

**Preliminary: three repetitions on a shared VM.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 5898.240 [5111.808, 5898.240] | 4325.376 [4325.376, 4325.376] | 5111.808 [4980.736, 5242.880] | 1.364× | 1.182× |
| recall-by-id | 7.808 [7.808, 7.936] | 3.968 [3.776, 4.032] | 1.856 [1.728, 1.888] | 1.968× | 0.468× |
| recent-for-agent | 5.376 [5.376, 5.376] | 20.480 [20.480, 20.992] | 3.904 [3.904, 3.904] | 0.263× | 0.191× |
| update | 5767.168 [4325.376, 5898.240] | 4325.376 [4325.376, 4325.376] | 4849.664 [4128.768, 5111.808] | 1.333× | 1.121× |
| mixed-90-10 | 11.776 [11.776, 12.032] | 6.528 [6.400, 6.656] | 3.264 [3.200, 3.264] | 1.804× | 0.500× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 11010.048 | 14942.208 | 39478.652 | 164 | 164 |
| golsm | remember | 22020.096 | 27262.976 | 103754.758 | 200 | 201 |
| lmdb | remember | 49283.072 | 54525.952 | 141716.790 | 81 | 82 |
| kiban | recall-by-id | 15.104 | 31.232 | 1057.207 | 107,298 | 128,563 |
| golsm | recall-by-id | 16.896 | 49.152 | 15331.954 | 158,157 | 211,128 |
| lmdb | recall-by-id | 3.520 | 15.360 | 280.435 | 281,752 | 498,169 |
| kiban | recent-for-agent | 12.032 | 30.720 | 620.751 | 76,256 | 176,503 |
| golsm | recent-for-agent | 167.936 | 802.816 | 9338.487 | 26,252 | 33,519 |
| lmdb | recent-for-agent | 8.064 | 16.896 | 128.190 | 87,618 | 244,705 |
| kiban | update | 11534.336 | 24641.536 | 59816.385 | 169 | 169 |
| golsm | update | 22020.096 | 27262.976 | 53774.004 | 201 | 201 |
| lmdb | update | 47185.920 | 51380.224 | 139971.893 | 92 | 92 |
| kiban | mixed-90-10 | 5898.240 | 18874.368 | 43021.069 | 1,972 | 1,981 |
| golsm | mixed-90-10 | 6029.312 | 20971.520 | 88592.213 | 1,997 | 2,013 |
| lmdb | mixed-90-10 | 30932.992 | 47185.920 | 107679.069 | 918 | 920 |

## Base N=1,000,000, fast

**Preliminary: three repetitions on a shared VM.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 1.216 [1.216, 1.248] | 3.072 [3.072, 3.072] | 10.752 [10.752, 10.752] | 0.396× | 3.500× |
| recall-by-id | 7.680 [7.680, 7.936] | 4.096 [3.968, 4.224] | 1.824 [1.792, 1.856] | 1.875× | 0.445× |
| recent-for-agent | 5.376 [5.376, 5.376] | 23.552 [23.040, 23.552] | 3.904 [3.904, 3.904] | 0.228× | 0.166× |
| update | 1.856 [1.728, 1.888] | 3.200 [3.200, 3.264] | 14.848 [14.592, 14.848] | 0.580× | 4.640× |
| mixed-90-10 | 8.192 [8.192, 8.448] | 4.608 [4.480, 4.736] | 1.888 [1.856, 1.984] | 1.778× | 0.410× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 9.728 | 19.968 | 225.001 | 269,722 | 592,119 |
| golsm | remember | 24.576 | 10747.904 | 364039.525 | 16,658 | 17,830 |
| lmdb | remember | 26.112 | 41.984 | 474.097 | 67,302 | 81,817 |
| kiban | recall-by-id | 14.080 | 32.256 | 1126.355 | 114,306 | 138,851 |
| golsm | recall-by-id | 18.944 | 69.632 | 15292.491 | 137,000 | 177,618 |
| lmdb | recall-by-id | 3.200 | 13.312 | 52.799 | 298,572 | 528,532 |
| kiban | recent-for-agent | 11.776 | 32.768 | 314.848 | 76,375 | 175,890 |
| golsm | recent-for-agent | 172.032 | 1146.880 | 6932.076 | 24,332 | 30,268 |
| lmdb | recent-for-agent | 8.192 | 17.920 | 301.232 | 86,865 | 240,370 |
| kiban | update | 10.752 | 20.480 | 239.939 | 218,619 | 430,664 |
| golsm | update | 26.624 | 11010.048 | 614398.891 | 15,099 | 16,118 |
| lmdb | update | 30.720 | 63.488 | 362794.198 | 15,371 | 16,111 |
| kiban | mixed-90-10 | 15.104 | 33.792 | 855.200 | 117,554 | 145,276 |
| golsm | mixed-90-10 | 22.528 | 196.608 | 40593.803 | 78,924 | 94,844 |
| lmdb | mixed-90-10 | 19.456 | 31.744 | 131348.998 | 133,140 | 171,270 |

## GoLSM maintenance counters

These counters are scoped to the foreground-open database instance, including its initial and final verification; compaction may overlap blocked API calls. They are not an additive CPU-time profile.

| Base N | Mode | Compactions | Total compaction seconds | Flushes |
| ---: | --- | ---: | ---: | ---: |
| 10,000 | durable | 6 | 0.364 [0.339, 0.459] | 21 |
| 10,000 | fast | 6 | 0.325 [0.289, 0.378] | 21 |
| 1,000,000 | durable | 9 | 0.530 [0.517, 0.558] | 30 |
| 1,000,000 | fast | 68 | 4.282 [4.213, 4.298] | 210 |

## Trial loads and resources

| Base N | Mode | Trial | Engine | Load median / max | Gate | End RSS MiB | Peak RSS MiB | Disk apparent MiB | Disk allocated MiB |
| ---: | --- | ---: | --- | ---: | --- | ---: | ---: | ---: | ---: |
| 10,000 | durable | 1 | golsm | 1.98 / 2.25 | pass | 37.8 | 37.8 | 9.5 | 9.5 |
| 10,000 | durable | 1 | kiban | 1.53 / 1.75 | pass | 36.8 | 44.2 | 7.6 | 7.6 |
| 10,000 | durable | 1 | lmdb | 1.53 / 2.25 | pass | 24.7 | 25.1 | 15.8 | 15.8 |
| 10,000 | fast | 1 | golsm | 1.66 / 2.09 | pass | 35.7 | 39.0 | 9.5 | 9.5 |
| 10,000 | fast | 1 | kiban | 1.66 / 1.66 | pass | 36.6 | 44.2 | 7.6 | 7.6 |
| 10,000 | fast | 1 | lmdb | 2.09 / 2.09 | pass | 24.5 | 25.0 | 15.8 | 15.8 |
| 10,000 | durable | 2 | golsm | 3.63 / 4.59 | preliminary | 34.2 | 38.8 | 9.5 | 9.5 |
| 10,000 | durable | 2 | kiban | 2.40 / 3.28 | pass | 26.4 | 45.1 | 7.6 | 7.6 |
| 10,000 | durable | 2 | lmdb | 3.76 / 4.93 | preliminary | 24.6 | 24.9 | 15.8 | 15.8 |
| 10,000 | fast | 2 | golsm | 3.77 / 3.77 | preliminary | 35.0 | 36.9 | 9.5 | 9.5 |
| 10,000 | fast | 2 | kiban | 3.85 / 3.93 | preliminary | 36.8 | 44.3 | 7.6 | 7.6 |
| 10,000 | fast | 2 | lmdb | 3.77 / 3.77 | preliminary | 24.6 | 25.1 | 15.8 | 15.8 |
| 10,000 | durable | 3 | golsm | 2.39 / 2.59 | pass | 36.1 | 36.9 | 9.5 | 9.5 |
| 10,000 | durable | 3 | kiban | 2.80 / 3.79 | pass | 36.7 | 44.2 | 7.6 | 7.6 |
| 10,000 | durable | 3 | lmdb | 1.43 / 2.33 | pass | 24.6 | 25.0 | 15.8 | 15.8 |
| 10,000 | fast | 3 | golsm | 1.18 / 1.18 | pass | 35.9 | 39.5 | 9.5 | 9.5 |
| 10,000 | fast | 3 | kiban | 1.18 / 1.18 | pass | 36.7 | 44.3 | 7.6 | 7.6 |
| 10,000 | fast | 3 | lmdb | 1.18 / 1.18 | pass | 24.5 | 25.0 | 15.8 | 15.8 |
| 10,000 | durable | 4 | golsm | 2.13 / 2.33 | pass | 36.2 | 51.2 | 9.5 | 9.5 |
| 10,000 | durable | 4 | kiban | 2.41 / 2.82 | pass | 36.6 | 44.9 | 7.6 | 7.6 |
| 10,000 | durable | 4 | lmdb | 1.27 / 1.99 | pass | 24.6 | 25.0 | 15.8 | 15.8 |
| 10,000 | fast | 4 | golsm | 1.08 / 1.08 | pass | 38.0 | 38.0 | 9.5 | 9.5 |
| 10,000 | fast | 4 | kiban | 1.18 / 1.18 | pass | 36.6 | 44.2 | 7.6 | 7.6 |
| 10,000 | fast | 4 | lmdb | 1.08 / 1.08 | pass | 24.5 | 25.0 | 15.8 | 15.8 |
| 10,000 | durable | 5 | golsm | 2.15 / 2.22 | pass | 36.6 | 36.9 | 9.5 | 9.5 |
| 10,000 | durable | 5 | kiban | 1.91 / 2.24 | pass | 36.7 | 44.4 | 7.6 | 7.6 |
| 10,000 | durable | 5 | lmdb | 1.34 / 2.04 | pass | 24.5 | 25.0 | 15.8 | 15.8 |
| 10,000 | fast | 5 | golsm | 1.64 / 1.64 | pass | 36.7 | 38.9 | 9.5 | 9.5 |
| 10,000 | fast | 5 | kiban | 1.52 / 1.52 | pass | 35.9 | 44.4 | 7.6 | 7.6 |
| 10,000 | fast | 5 | lmdb | 1.64 / 1.64 | pass | 24.7 | 25.1 | 15.8 | 15.8 |
| 1,000,000 | durable | 1 | golsm | 1.96 / 2.25 | pass | 454.3 | 1375.3 | 351.7 | 351.7 |
| 1,000,000 | durable | 1 | kiban | 1.96 / 2.07 | pass | 139.8 | 1619.7 | 323.9 | 323.9 |
| 1,000,000 | durable | 1 | lmdb | 1.25 / 1.78 | pass | 826.1 | 1333.3 | 807.5 | 807.5 |
| 1,000,000 | fast | 1 | golsm | 1.38 / 1.61 | pass | 510.0 | 1133.4 | 412.0 | 412.0 |
| 1,000,000 | fast | 1 | kiban | 1.16 / 1.17 | pass | 280.2 | 1618.8 | 376.1 | 376.2 |
| 1,000,000 | fast | 1 | lmdb | 1.81 / 1.94 | pass | 898.9 | 1333.4 | 879.3 | 879.3 |
| 1,000,000 | durable | 2 | golsm | 1.90 / 2.29 | pass | 449.1 | 1426.9 | 351.7 | 351.7 |
| 1,000,000 | durable | 2 | kiban | 1.85 / 2.16 | pass | 128.5 | 1611.7 | 323.9 | 323.9 |
| 1,000,000 | durable | 2 | lmdb | 1.27 / 2.31 | pass | 825.9 | 1333.2 | 807.5 | 807.5 |
| 1,000,000 | fast | 2 | golsm | 1.10 / 1.25 | pass | 517.3 | 1406.8 | 412.0 | 412.0 |
| 1,000,000 | fast | 2 | kiban | 1.04 / 1.05 | pass | 281.2 | 1618.8 | 376.1 | 376.2 |
| 1,000,000 | fast | 2 | lmdb | 1.89 / 1.95 | pass | 899.0 | 1333.4 | 879.3 | 879.3 |
| 1,000,000 | durable | 3 | golsm | 2.05 / 2.15 | pass | 451.1 | 1356.2 | 351.7 | 351.7 |
| 1,000,000 | durable | 3 | kiban | 1.86 / 2.08 | pass | 94.6 | 1612.0 | 323.9 | 323.9 |
| 1,000,000 | durable | 3 | lmdb | 1.33 / 1.79 | pass | 826.2 | 1333.5 | 807.5 | 807.5 |
| 1,000,000 | fast | 3 | golsm | 1.51 / 1.56 | pass | 515.3 | 1276.8 | 412.0 | 412.0 |
| 1,000,000 | fast | 3 | kiban | 1.23 / 1.25 | pass | 321.9 | 1618.7 | 376.1 | 376.2 |
| 1,000,000 | fast | 3 | lmdb | 1.72 / 1.92 | pass | 898.8 | 1333.2 | 879.3 | 879.3 |
