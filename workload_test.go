package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

type latency struct{ P50, P95, P99 int64 }

func percentiles(samples []int64) latency {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) == 0 {
		return latency{}
	}
	at := func(p int) int64 { return samples[(len(samples)-1)*p/100] }
	return latency{at(50), at(95), at(99)}
}

type workloadResult struct {
	Name                                                    string
	Trial, ReadPercent, Operations, InitialKeys, ValueBytes int
	SyncWrites                                              bool
	Seconds, OpsPerSecond                                   float64
	ReadLatencyNS, WriteLatencyNS                           latency
	BeforeCompaction, AfterCompaction                       Metrics
	CompactionNS                                            int64
}

func TestWorkloadReport(t *testing.T) {
	output := os.Getenv("KVSTORE_REPORT_PATH")
	if output == "" {
		t.Skip("set KVSTORE_REPORT_PATH to write reproducible workload results")
	}
	var results []workloadResult
	for trial := 1; trial <= 3; trial++ {
		for _, reads := range []int{50, 95, 100} {
			dir := t.TempDir()
			db, err := Open(dir, Options{CompactionInterval: -1})
			must(t, err)
			keys := benchmarkKeys(20000)
			for _, k := range keys {
				must(t, db.Put(k, benchValue))
			}
			must(t, db.Flush())
			r := rand.New(rand.NewSource(int64(42 + trial)))
			readTimes := make([]int64, 0, 50000)
			writeTimes := make([]int64, 0, 50000)
			started := time.Now()
			for i := 0; i < 50000; i++ {
				k := keys[r.Intn(len(keys))]
				read := r.Intn(100) < reads
				opStart := time.Now()
				if read {
					v, ok, err := db.Get(k)
					if err != nil || !ok || v != benchValue {
						t.Fatal(k, ok, err)
					}
					readTimes = append(readTimes, time.Since(opStart).Nanoseconds())
				} else {
					must(t, db.Put(k, benchValue))
					writeTimes = append(writeTimes, time.Since(opStart).Nanoseconds())
				}
			}
			elapsed := time.Since(started)
			before := db.Stats()
			compactStart := time.Now()
			must(t, db.Compact())
			compactNS := time.Since(compactStart).Nanoseconds()
			after := db.Stats()
			must(t, db.Close())
			db, err = Open(dir, Options{CompactionInterval: -1})
			must(t, err)
			for _, k := range keys {
				want(t, db, k, benchValue, true)
			}
			must(t, db.Close())
			results = append(results, workloadResult{Name: fmt.Sprintf("read%d-write%d", reads, 100-reads), Trial: trial, ReadPercent: reads, Operations: 50000, InitialKeys: 20000, ValueBytes: 100, Seconds: elapsed.Seconds(), OpsPerSecond: 50000 / elapsed.Seconds(), ReadLatencyNS: percentiles(readTimes), WriteLatencyNS: percentiles(writeTimes), BeforeCompaction: before, AfterCompaction: after, CompactionNS: compactNS})
		}
	}
	report := struct {
		GoVersion, OS, Arch string
		CPUs                int
		Results             []workloadResult
	}{runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), results}
	b, err := json.MarshalIndent(report, "", "  ")
	must(t, err)
	must(t, os.MkdirAll(filepath.Dir(output), 0755))
	must(t, os.WriteFile(output, append(b, '\n'), 0644))
}
