# GoLSM vs Kiban vs LMDB: matched engine workload

All engines ran in one release executable with in-process calls on pancakes. These are new measurements, not the post’s hardware results. GoLSM uses a Go C archive; its measured latency includes the foreign-function call and result copy. The original Kiban/LMDB adapters and the upstream generator, oracle, and histogram are retained.

Validated **48 trials**, **8,880,000 timed calls**, **2,790,000 immediate write read-backs**, and **19,590,000 records after new-process reopen**. No value errors.

The original direct Kiban handle buffers its WAL and defers explicit flushing to the excluded finish step; GoLSM retains its automatic 1,000-operation flush and background compactor. Thus fast p50 is foreground acknowledgment latency with different buffering/maintenance policies, not equal sustained compaction-drained capacity. GoLSM also pays its measured C-archive bridge overhead.

Cells are medians of trial statistics, with trial ranges where shown. Latency histogram quantization is under 3.125%. No trials were dropped. These are shared-VM results; the 1M results remain preliminary with three repetitions. Full settings and limitations are in the benchmark README.

## Base N=10,000, durable

**Preliminary: one or more trials exceeded the retained load/coverage gate. All trials are included.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 4194.304 [3801.088, 5767.168] | 3735.552 [3604.480, 3932.160] | 4587.520 [4587.520, 4849.664] | 1.123× | 1.228× |
| recall-by-id | 1.056 [1.056, 1.696] | 4.224 [2.560, 4.736] | 1.152 [1.152, 1.984] | 0.250× | 0.273× |
| recent-for-agent | 4.864 [4.864, 8.704] | 26.112 [20.992, 167.936] | 3.840 [3.776, 7.040] | 0.186× | 0.147× |
| update | 3866.624 [3801.088, 5767.168] | 3670.016 [3604.480, 3801.088] | 4325.376 [4325.376, 4456.448] | 1.054× | 1.179× |
| mixed-90-10 | 2.304 [1.664, 2.816] | 4.096 [3.840, 5.888] | 2.176 [2.048, 2.944] | 0.562× | 0.531× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 20971.520 | 32505.856 | 76287.138 | 181 | 182 |
| golsm | remember | 28311.552 | 50331.648 | 159924.651 | 186 | 186 |
| lmdb | remember | 47185.920 | 53477.376 | 193749.270 | 97 | 97 |
| kiban | recall-by-id | 2.432 | 9.728 | 38.131 | 409,292 | 930,768 |
| golsm | recall-by-id | 26.112 | 638.976 | 2022.651 | 90,613 | 132,679 |
| lmdb | recall-by-id | 2.176 | 6.528 | 36.689 | 387,078 | 830,942 |
| kiban | recent-for-agent | 9.984 | 20.480 | 74.810 | 82,057 | 200,916 |
| golsm | recent-for-agent | 434.176 | 2064.384 | 31782.752 | 12,006 | 14,326 |
| lmdb | recent-for-agent | 8.192 | 15.872 | 35.937 | 88,767 | 249,648 |
| kiban | update | 26214.400 | 31981.568 | 137096.712 | 188 | 189 |
| golsm | update | 28311.552 | 46137.344 | 217094.444 | 192 | 194 |
| lmdb | update | 46137.344 | 52428.800 | 241992.512 | 108 | 108 |
| kiban | mixed-90-10 | 7471.104 | 26214.400 | 154653.564 | 1,878 | 1,885 |
| golsm | mixed-90-10 | 7733.248 | 28311.552 | 39939.346 | 1,997 | 2,006 |
| lmdb | mixed-90-10 | 28311.552 | 45088.768 | 49350.032 | 1,081 | 1,084 |

## Base N=10,000, fast

**Preliminary: one or more trials exceeded the retained load/coverage gate. All trials are included.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 1.184 [1.152, 1.792] | 2.752 [2.688, 4.096] | 8.960 [8.704, 12.800] | 0.430× | 3.256× |
| recall-by-id | 1.008 [0.960, 1.600] | 3.008 [2.944, 4.608] | 1.152 [1.120, 1.824] | 0.335× | 0.383× |
| recent-for-agent | 7.168 [4.736, 8.448] | 44.032 [43.008, 69.632] | 3.776 [3.776, 7.040] | 0.163× | 0.086× |
| update | 1.984 [1.504, 2.240] | 2.944 [2.752, 4.096] | 9.216 [8.960, 13.056] | 0.674× | 3.130× |
| mixed-90-10 | 0.832 [0.784, 1.312] | 3.648 [3.520, 5.376] | 1.152 [1.088, 1.856] | 0.228× | 0.316× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 9.984 | 19.968 | 114.224 | 271,136 | 613,397 |
| golsm | remember | 27.136 | 802.816 | 37866.691 | 25,861 | 28,560 |
| lmdb | remember | 24.064 | 39.936 | 57.999 | 77,855 | 97,176 |
| kiban | recall-by-id | 2.176 | 5.632 | 36.759 | 419,315 | 982,847 |
| golsm | recall-by-id | 23.040 | 139.264 | 604.901 | 182,711 | 252,459 |
| lmdb | recall-by-id | 2.240 | 11.520 | 16.471 | 380,587 | 836,682 |
| kiban | recent-for-agent | 15.616 | 34.816 | 144.901 | 61,484 | 138,409 |
| golsm | recent-for-agent | 208.896 | 385.024 | 1930.880 | 15,171 | 17,237 |
| lmdb | recent-for-agent | 7.936 | 18.432 | 33.743 | 88,770 | 253,813 |
| kiban | update | 14.848 | 30.720 | 212.477 | 182,248 | 366,900 |
| golsm | update | 29.696 | 720.896 | 37453.727 | 25,699 | 28,343 |
| lmdb | update | 19.456 | 35.840 | 55.193 | 81,523 | 104,385 |
| kiban | mixed-90-10 | 2.816 | 14.848 | 32.390 | 385,945 | 877,072 |
| golsm | mixed-90-10 | 27.648 | 135.168 | 32448.310 | 105,198 | 128,803 |
| lmdb | mixed-90-10 | 11.264 | 23.040 | 33.773 | 282,679 | 491,530 |

## Base N=1,000,000, durable

**Preliminary: three repetitions on a shared VM.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 4980.736 [4980.736, 5767.168] | 3604.480 [3604.480, 4718.592] | 4849.664 [3801.088, 4849.664] | 1.382× | 1.345× |
| recall-by-id | 7.552 [7.296, 7.808] | 4.032 [3.904, 4.480] | 1.888 [1.792, 1.920] | 1.873× | 0.468× |
| recent-for-agent | 5.504 [5.248, 6.144] | 23.552 [13.568, 62.464] | 3.968 [3.840, 4.032] | 0.234× | 0.168× |
| update | 4980.736 [4980.736, 5898.240] | 3670.016 [3604.480, 4718.592] | 4456.448 [3473.408, 4456.448] | 1.357× | 1.214× |
| mixed-90-10 | 10.752 [10.240, 12.032] | 6.528 [5.888, 6.912] | 3.072 [3.008, 3.136] | 1.647× | 0.471× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 15728.640 | 47185.920 | 142495.522 | 170 | 170 |
| golsm | remember | 27787.264 | 38797.312 | 160504.741 | 166 | 198 |
| lmdb | remember | 51380.224 | 62914.560 | 282967.419 | 80 | 80 |
| kiban | recall-by-id | 16.128 | 32.256 | 650.376 | 108,643 | 132,104 |
| golsm | recall-by-id | 19.968 | 180.224 | 7807.633 | 132,628 | 172,794 |
| lmdb | recall-by-id | 5.504 | 20.480 | 1395.242 | 258,091 | 457,067 |
| kiban | recent-for-agent | 13.056 | 43.008 | 2512.448 | 71,993 | 164,491 |
| golsm | recent-for-agent | 147.456 | 2031.616 | 8769.000 | 21,734 | 26,265 |
| lmdb | recent-for-agent | 10.496 | 29.696 | 3705.949 | 76,683 | 200,189 |
| kiban | update | 15466.496 | 23068.672 | 155751.451 | 166 | 167 |
| golsm | update | 27262.976 | 37748.736 | 112034.658 | 166 | 198 |
| lmdb | update | 49283.072 | 56623.104 | 199864.414 | 91 | 91 |
| kiban | mixed-90-10 | 6946.816 | 16252.928 | 187541.793 | 1,687 | 1,695 |
| golsm | mixed-90-10 | 7340.032 | 27262.976 | 292460.967 | 1,590 | 1,901 |
| lmdb | mixed-90-10 | 33030.144 | 49283.072 | 283369.252 | 906 | 908 |

## Base N=1,000,000, fast

**Preliminary: three repetitions on a shared VM.**

| Workload | Kiban p50 µs | GoLSM p50 µs | LMDB p50 µs | Kiban / GoLSM | LMDB / GoLSM |
| --- | ---: | ---: | ---: | ---: | ---: |
| remember | 1.280 [1.280, 1.856] | 2.880 [2.816, 2.944] | 12.544 [10.752, 13.056] | 0.444× | 4.356× |
| recall-by-id | 8.064 [7.936, 9.728] | 14.592 [13.312, 14.848] | 1.952 [1.856, 1.984] | 0.553× | 0.134× |
| recent-for-agent | 5.504 [5.504, 9.216] | 20.480 [16.384, 20.992] | 4.032 [3.968, 7.040] | 0.269× | 0.197× |
| update | 2.304 [1.952, 2.560] | 3.008 [2.752, 3.008] | 19.968 [18.432, 19.968] | 0.766× | 6.638× |
| mixed-90-10 | 8.448 [8.192, 10.496] | 14.592 [13.824, 15.104] | 2.432 [2.432, 2.752] | 0.579× | 0.167× |

Ratios above 1 favor GoLSM; below 1 favor the named competitor. These ratios compare p50 acknowledgment latency, not sustained throughput.

| Engine | Workload | p99 µs | p999 µs | Max µs | Verified ops/s | Call-only ops/s |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| kiban | remember | 12.544 | 22.016 | 249.857 | 239,971 | 522,525 |
| golsm | remember | 29.696 | 2883.584 | 45626.390 | 25,131 | 27,630 |
| lmdb | remember | 32.768 | 54.272 | 2179.995 | 57,742 | 71,051 |
| kiban | recall-by-id | 20.480 | 53.248 | 11094.348 | 98,849 | 119,715 |
| golsm | recall-by-id | 43.008 | 270.336 | 9968.792 | 51,781 | 57,103 |
| lmdb | recall-by-id | 4.032 | 19.456 | 557.803 | 254,894 | 465,343 |
| kiban | recent-for-agent | 13.568 | 36.864 | 3039.541 | 68,018 | 150,654 |
| golsm | recent-for-agent | 1277.952 | 8650.752 | 5298408.729 | 3,911 | 4,056 |
| lmdb | recent-for-agent | 10.496 | 28.160 | 2184.605 | 76,308 | 199,509 |
| kiban | update | 15.104 | 53.248 | 3503.960 | 165,855 | 323,601 |
| golsm | update | 30.208 | 4587.520 | 49870.389 | 24,523 | 26,952 |
| lmdb | update | 19398.656 | 25690.112 | 417090.651 | 702 | 704 |
| kiban | mixed-90-10 | 17.920 | 39.936 | 6770.915 | 110,196 | 137,348 |
| golsm | mixed-90-10 | 55.296 | 253.952 | 43523.365 | 45,069 | 49,370 |
| lmdb | mixed-90-10 | 7995.392 | 18350.080 | 252420.168 | 6,564 | 6,665 |

## GoLSM maintenance counters

These counters are scoped to the foreground-open database instance, including its initial and final verification; compaction may overlap blocked API calls. They are not an additive CPU-time profile.

| Base N | Mode | Compactions | Total compaction seconds | Flushes |
| ---: | --- | ---: | ---: | ---: |
| 10,000 | durable | 3 | 0.336 [0.233, 0.633] | 22 |
| 10,000 | fast | 0 | 0.000 [0.000, 0.000] | 21 |
| 1,000,000 | durable | 6 | 29.297 [24.540, 36.406] | 34 |
| 1,000,000 | fast | 2 | 10.756 [10.713, 10.899] | 210 |

## Trial loads and resources

| Base N | Mode | Trial | Engine | Load median / max | Gate | End RSS MiB | Peak RSS MiB | Disk apparent MiB | Disk allocated MiB |
| ---: | --- | ---: | --- | ---: | --- | ---: | ---: | ---: | ---: |
| 10,000 | durable | 1 | golsm | 2.26 / 2.39 | pass | 40.0 | 40.0 | 7.9 | 7.9 |
| 10,000 | durable | 1 | kiban | 1.55 / 1.89 | pass | 36.8 | 45.1 | 7.6 | 7.6 |
| 10,000 | durable | 1 | lmdb | 1.58 / 2.27 | pass | 24.5 | 25.0 | 15.8 | 15.8 |
| 10,000 | fast | 1 | golsm | 1.15 / 1.15 | pass | 46.6 | 46.6 | 10.4 | 10.5 |
| 10,000 | fast | 1 | kiban | 1.15 / 1.15 | pass | 36.6 | 44.0 | 7.6 | 7.6 |
| 10,000 | fast | 1 | lmdb | 1.15 / 1.15 | pass | 24.6 | 25.0 | 15.8 | 15.8 |
| 10,000 | durable | 2 | golsm | 1.87 / 2.47 | pass | 38.0 | 38.0 | 7.8 | 7.9 |
| 10,000 | durable | 2 | kiban | 1.59 / 1.88 | pass | 36.4 | 44.2 | 7.6 | 7.6 |
| 10,000 | durable | 2 | lmdb | 1.60 / 2.43 | pass | 24.6 | 25.1 | 15.8 | 15.8 |
| 10,000 | fast | 2 | golsm | 1.56 / 1.56 | pass | 46.6 | 46.6 | 10.4 | 10.5 |
| 10,000 | fast | 2 | kiban | 1.58 / 1.61 | pass | 26.6 | 44.4 | 7.6 | 7.6 |
| 10,000 | fast | 2 | lmdb | 1.56 / 1.56 | pass | 24.5 | 25.0 | 15.8 | 15.8 |
| 10,000 | durable | 3 | golsm | 3.57 / 3.80 | preliminary | 36.6 | 36.6 | 8.6 | 8.6 |
| 10,000 | durable | 3 | kiban | 2.23 / 2.48 | pass | 26.2 | 44.9 | 7.6 | 7.6 |
| 10,000 | durable | 3 | lmdb | 3.29 / 4.12 | preliminary | 24.6 | 25.1 | 15.8 | 15.8 |
| 10,000 | fast | 3 | golsm | 3.95 / 3.95 | preliminary | 42.1 | 42.4 | 10.4 | 10.5 |
| 10,000 | fast | 3 | kiban | 3.95 / 3.95 | preliminary | 24.9 | 42.7 | 7.6 | 7.6 |
| 10,000 | fast | 3 | lmdb | 3.87 / 3.87 | preliminary | 24.4 | 24.9 | 15.8 | 15.8 |
| 10,000 | durable | 4 | golsm | 4.16 / 4.22 | preliminary | 38.5 | 38.5 | 8.4 | 8.4 |
| 10,000 | durable | 4 | kiban | 4.05 / 4.11 | preliminary | 35.1 | 42.8 | 7.6 | 7.6 |
| 10,000 | durable | 4 | lmdb | 3.65 / 4.09 | preliminary | 24.4 | 24.8 | 15.8 | 15.8 |
| 10,000 | fast | 4 | golsm | 3.32 / 3.32 | preliminary | 40.5 | 40.5 | 10.4 | 10.5 |
| 10,000 | fast | 4 | kiban | 3.32 / 3.32 | preliminary | 26.3 | 44.4 | 7.6 | 7.6 |
| 10,000 | fast | 4 | lmdb | 3.38 / 3.38 | preliminary | 24.6 | 25.0 | 15.8 | 15.8 |
| 10,000 | durable | 5 | golsm | 5.39 / 6.29 | preliminary | 37.5 | 37.5 | 8.7 | 8.7 |
| 10,000 | durable | 5 | kiban | 3.89 / 4.97 | preliminary | 36.7 | 44.1 | 7.6 | 7.6 |
| 10,000 | durable | 5 | lmdb | 1.64 / 3.74 | pass | 24.7 | 25.0 | 15.8 | 15.8 |
| 10,000 | fast | 5 | golsm | 1.12 / 1.19 | pass | 40.5 | 40.5 | 10.4 | 10.5 |
| 10,000 | fast | 5 | kiban | 1.12 / 1.12 | pass | 36.7 | 44.2 | 7.6 | 7.6 |
| 10,000 | fast | 5 | lmdb | 1.19 / 1.19 | pass | 24.8 | 25.0 | 15.8 | 15.8 |
| 1,000,000 | durable | 1 | golsm | 2.10 / 2.34 | pass | 465.0 | 1395.9 | 345.1 | 345.1 |
| 1,000,000 | durable | 1 | kiban | 2.28 / 2.46 | pass | 71.8 | 1620.2 | 323.9 | 323.9 |
| 1,000,000 | durable | 1 | lmdb | 1.33 / 2.18 | pass | 826.0 | 1333.4 | 807.5 | 807.5 |
| 1,000,000 | fast | 1 | golsm | 2.19 / 2.79 | pass | 173.2 | 1245.8 | 375.7 | 375.7 |
| 1,000,000 | fast | 1 | kiban | 2.49 / 2.55 | pass | 282.1 | 1617.3 | 376.1 | 376.2 |
| 1,000,000 | fast | 1 | lmdb | 2.86 / 3.23 | pass | 899.0 | 1333.4 | 879.3 | 879.3 |
| 1,000,000 | durable | 2 | golsm | 2.14 / 2.78 | pass | 413.0 | 1382.2 | 344.9 | 344.9 |
| 1,000,000 | durable | 2 | kiban | 2.63 / 3.17 | pass | 119.5 | 1611.8 | 323.9 | 323.9 |
| 1,000,000 | durable | 2 | lmdb | 2.01 / 3.13 | pass | 826.0 | 1333.3 | 807.5 | 807.5 |
| 1,000,000 | fast | 2 | golsm | 1.72 / 1.83 | pass | 201.4 | 1267.6 | 375.7 | 375.7 |
| 1,000,000 | fast | 2 | kiban | 1.38 / 1.43 | pass | 314.1 | 1612.7 | 376.1 | 376.2 |
| 1,000,000 | fast | 2 | lmdb | 2.72 / 2.96 | pass | 898.9 | 1333.3 | 879.3 | 879.3 |
| 1,000,000 | durable | 3 | golsm | 2.38 / 2.98 | pass | 464.0 | 1329.3 | 345.7 | 345.7 |
| 1,000,000 | durable | 3 | kiban | 2.09 / 3.08 | pass | 133.9 | 1617.8 | 323.9 | 323.9 |
| 1,000,000 | durable | 3 | lmdb | 1.32 / 2.04 | pass | 826.1 | 1333.4 | 807.5 | 807.5 |
| 1,000,000 | fast | 3 | golsm | 2.35 / 2.50 | pass | 213.7 | 1253.9 | 375.7 | 375.7 |
| 1,000,000 | fast | 3 | kiban | 1.62 / 1.67 | pass | 285.1 | 1616.8 | 376.1 | 376.2 |
| 1,000,000 | fast | 3 | lmdb | 2.98 / 3.54 | pass | 899.0 | 1333.4 | 879.3 | 879.3 |
