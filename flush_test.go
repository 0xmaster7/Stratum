package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFrozenMemtableVisibilityAndBackpressure(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 2, CompactionInterval: -1})
	// Hold maintenance so the background flush cannot finish before assertions.
	db.maintenanceMu.Lock()
	locked := true
	defer func() {
		if locked {
			db.maintenanceMu.Unlock()
		}
	}()
	must(t, db.Put("a", "frozen"))
	must(t, db.Put("b", "frozen"))
	want(t, db, "a", "frozen", true)
	must(t, db.Put("a", "active"))
	want(t, db, "a", "active", true)
	rows, err := db.Scan("", "", 10)
	must(t, err)
	if len(rows) != 2 || rows[0].Value != "active" || rows[1].Key != "b" {
		t.Fatal(rows)
	}
	blocked := make(chan error, 1)
	go func() { blocked <- db.Delete("b") }()
	select {
	case err := <-blocked:
		t.Fatalf("writer escaped backpressure: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	// Readers continue while a writer waits for the immutable flush.
	want(t, db, "a", "active", true)
	db.maintenanceMu.Unlock()
	locked = false
	select {
	case err := <-blocked:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("writer never resumed")
	}
	must(t, db.Flush())
	want(t, db, "b", "", false)
}

func TestFlushBuildAllowsForegroundOperations(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 10000, CompactionInterval: -1})
	must(t, db.Put("key", "frozen"))
	db.mu.Lock()
	must(t, db.rotateLocked())
	target := db.frozen.generation
	db.mu.Unlock()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		db.maintenanceMu.Lock()
		defer db.maintenanceMu.Unlock()
		done <- db.flushTo(target, func() { close(started); <-release })
	}()
	<-started
	foreground := make(chan error, 1)
	go func() {
		if value, ok, err := db.Get("key"); err != nil || !ok || value != "frozen" {
			foreground <- fmt.Errorf("frozen read failed")
			return
		}
		foreground <- db.Put("key", "active")
	}()
	select {
	case err := <-foreground:
		must(t, err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("flush build blocked foreground")
	}
	close(release)
	must(t, <-done)
	want(t, db, "key", "active", true)
	must(t, db.Flush())
	want(t, db, "key", "active", true)
}

func TestCrashRecoveryWALGenerations(t *testing.T) {
	if path := os.Getenv("GOLSM_CRASH_DIR"); path != "" {
		stage := os.Getenv("GOLSM_CRASH_STAGE")
		db, err := Open(path, Options{SyncWrites: true, FlushThreshold: 10000, CompactionInterval: -1})
		must(t, err)
		must(t, db.Put("key", "base"))
		must(t, db.Put("gone", "base"))
		must(t, db.Flush())
		must(t, db.Put("key", "frozen"))
		must(t, db.Delete("gone"))
		path = filepath.Join(db.dir, fmt.Sprintf("wal_%020d.log", db.walGeneration))
		if stage == "rename" {
			must(t, db.wal.Sync())
			must(t, os.Rename(filepath.Join(db.dir, "data.log"), path))
			must(t, syncDir(db.dir))
			os.Exit(0)
		}
		db.mu.Lock()
		must(t, db.rotateLocked())
		db.mu.Unlock()
		if stage == "frozen" {
			os.Exit(0)
		}
		must(t, db.Put("key", "active"))
		if stage == "published" {
			// Emulate obsolete segments surviving a durable manifest publication.
			must(t, db.Flush())
			must(t, os.WriteFile(path, []byte("PUT key stale\nPUT gone resurrected\n"), 0644))
			must(t, syncDir(db.dir))
		}
		// Exit without Close: recovery must combine immutable and active WALs.
		os.Exit(0)
	}
	for _, stage := range []string{"rename", "frozen", "active", "published"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecoveryWALGenerations$")
			cmd.Env = append(os.Environ(), "GOLSM_CRASH_DIR="+dir, "GOLSM_CRASH_STAGE="+stage)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child: %v\n%s", err, out)
			}
			db, err := Open(dir, Options{CompactionInterval: -1})
			must(t, err)
			defer db.Close()
			expected := "active"
			if stage == "rename" || stage == "frozen" {
				expected = "frozen"
			}
			want(t, db, "key", expected, true)
			want(t, db, "gone", "", false)
			must(t, db.Flush())
			must(t, db.Close())
			again, err := Open(dir, Options{})
			must(t, err)
			defer again.Close()
			want(t, again, "key", expected, true)
			want(t, again, "gone", "", false)
		})
	}
}
