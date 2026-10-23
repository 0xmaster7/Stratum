#!/bin/sh
# Run from the repository root. Results are overwritten; test data uses temp dirs.
set -eu
mkdir -p benchmarks
{
 date -u
 go version
 uname -a
 if command -v lscpu >/dev/null 2>&1; then lscpu; fi
 git rev-parse HEAD
 uptime
} > benchmarks/environment.txt
go vet ./...
go test -race -count=1 ./... > benchmarks/correctness.txt 2>&1
cat benchmarks/correctness.txt
go test -run '^$' -bench '^(BenchmarkPut|BenchmarkGet|BenchmarkGetParallel|BenchmarkMixed|BenchmarkScan100)$' -benchtime=1s -count=3 > benchmarks/micro.txt 2>&1
cat benchmarks/micro.txt
go test -run '^$' -bench '^(BenchmarkFlush1000|BenchmarkCompaction|BenchmarkRecovery20000)$' -benchtime=5x -count=3 > benchmarks/maintenance.txt 2>&1
cat benchmarks/maintenance.txt
KVSTORE_REPORT_PATH=benchmarks/workload.json go test -run '^TestWorkloadReport$' -count=1 -timeout=10m > benchmarks/workload-run.txt 2>&1
cat benchmarks/workload-run.txt
