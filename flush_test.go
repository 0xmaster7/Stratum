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
			// Construct the previous revision's legacy rename boundary.
			legacy := filepath.Join(db.dir, "data.log")
			must(t, os.Rename(db.wal.Name(), legacy))
			must(t, syncDir(db.dir))
			must(t, os.Rename(legacy, path))
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

func TestReappearingCheckpointedFilesAreIgnored(t *testing.T) {
	db := openTest(t, Options{CompactionInterval: -1})
	must(t, db.Put("key", "old"))
	must(t, db.Put("gone", "old"))
	must(t, db.Flush())
	obsolete := db.tables[0]
	checkpoint := db.flushedWAL
	must(t, db.Put("key", "new"))
	must(t, db.Delete("gone"))
	must(t, db.Compact())
	must(t, db.Close())
	// Model unlinks being lost across a crash after their replacement/checkpoint
	// was synced. Even corrupt obsolete files must not be decoded or replayed.
	paths := []string{obsolete.path, obsolete.indexPath, filepath.Join(db.dir, fmt.Sprintf("wal_%020d.log", checkpoint))}
	for _, path := range paths {
		must(t, os.WriteFile(path, []byte("obsolete corrupt bytes"), 0644))
	}
	reopened, err := Open(db.dir, Options{})
	must(t, err)
	defer reopened.Close()
	want(t, reopened, "key", "new", true)
	want(t, reopened, "gone", "", false)
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("obsolete file remains: %s: %v", path, err)
		}
	}
}

func TestLegacyActiveWALFollowsNumberedSegments(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "wal_00000000000000000001.log"), []byte("PUT key older\nPUT other retained\n"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "data.log"), []byte("PUT key newest\n"), 0644))
	db, err := Open(dir, Options{CompactionInterval: -1})
	must(t, err)
	defer db.Close()
	want(t, db, "key", "newest", true)
	want(t, db, "other", "retained", true)
	must(t, db.Flush())
	if filepath.Base(db.wal.Name()) == "data.log" {
		t.Fatal("rotation did not switch to a numbered active WAL")
	}
	must(t, db.Put("key", "after migration"))
	must(t, db.Close())
	again, err := Open(dir, Options{})
	must(t, err)
	defer again.Close()
	want(t, again, "key", "after migration", true)
}

func TestNumberedActiveWALWithPartialTail(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "wal_00000000000000000002.log")
	active := filepath.Join(dir, "wal_00000000000000000003.log")
	must(t, os.WriteFile(older, []byte("PUT key older\nPUT other retained\n"), 0644))
	valid := []byte("PUT key newest\n")
	must(t, os.WriteFile(active, append(valid, []byte("KVR")...), 0644))
	db, err := Open(dir, Options{CompactionInterval: -1})
	must(t, err)
	defer db.Close()
	if db.wal.Name() != active || db.walGeneration != 3 {
		t.Fatal("did not recover the highest numbered WAL as active")
	}
	st, err := os.Stat(active)
	must(t, err)
	if st.Size() != int64(len(valid)) {
		t.Fatal("partial tail retained")
	}
	want(t, db, "key", "newest", true)
	want(t, db, "other", "retained", true)
	must(t, db.Put("key", "appended"))
	must(t, db.Flush())
	must(t, db.Close())
	again, err := Open(dir, Options{})
	must(t, err)
	defer again.Close()
	want(t, again, "key", "appended", true)
}

func TestDuplicateWALGenerationRejected(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"wal_1.log", "wal_00000000000000000001.log"} {
		must(t, os.WriteFile(filepath.Join(dir, name), nil, 0644))
	}
	if db, err := Open(dir, Options{}); err == nil {
		db.Close()
		t.Fatal("ambiguous generations accepted")
	}
}
