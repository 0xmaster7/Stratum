package main

import (
	"reflect"
	"testing"
)

func TestScan(t *testing.T) {
	db := openTest(t, Options{})
	for _, k := range []string{"", "a", "b", "c", "d", "e"} {
		must(t, db.Put(k, "old"))
	}
	must(t, db.Flush())
	must(t, db.Put("b", "new"))
	must(t, db.Delete("c"))
	must(t, db.Flush())
	must(t, db.Delete("d"))
	must(t, db.Put("bb", "mem"))
	for _, tc := range []struct {
		start, end string
		limit      int
		keys       []string
	}{{"b", "e", 0, []string{"b", "bb"}}, {"", "", 0, []string{"", "a", "b", "bb", "e"}}, {"a", "", 2, []string{"a", "b"}}, {"a", "a", 0, []string{}}, {"z", "", 0, []string{}}} {
		rows, err := db.Scan(tc.start, tc.end, tc.limit)
		must(t, err)
		keys := make([]string, 0)
		for _, row := range rows {
			keys = append(keys, row.Key)
		}
		if !reflect.DeepEqual(keys, tc.keys) {
			t.Fatalf("scan %+v => %v", tc, keys)
		}
	}
	rows, err := db.Scan("b", "c", 0)
	must(t, err)
	if rows[0].Value != "new" || rows[1].Value != "mem" {
		t.Fatal(rows)
	}
	if _, err = db.Scan("z", "a", 0); err == nil {
		t.Fatal("invalid interval")
	}
	if _, err = db.Scan("", "", -1); err == nil {
		t.Fatal("negative limit")
	}
}

func TestScanViewInvalidation(t *testing.T) {
	db := openTest(t, Options{FlushThreshold: 10000})
	must(t, db.Put("a", "old"))
	check := func(want []Entry) {
		t.Helper()
		got, err := db.Scan("", "", 10)
		must(t, err)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	check([]Entry{{Key: "a", Value: "old"}})
	must(t, db.Put("a", "new"))
	check([]Entry{{Key: "a", Value: "new"}})
	must(t, db.Put("b", "second"))
	check([]Entry{{Key: "a", Value: "new"}, {Key: "b", Value: "second"}})
	must(t, db.Delete("a"))
	check([]Entry{{Key: "b", Value: "second"}})
	must(t, db.Flush())
	check([]Entry{{Key: "b", Value: "second"}})
	must(t, db.Put("a", "again"))
	check([]Entry{{Key: "a", Value: "again"}, {Key: "b", Value: "second"}})
}

func TestScanSkipsNonoverlappingTables(t *testing.T) {
	db := openTest(t, Options{})
	for _, key := range []string{"a", "m", "z"} {
		must(t, db.Put(key, key))
		must(t, db.Flush())
	}
	// Invalid test-only views make accidental traversal fail without modifying files.
	for _, table := range db.tables {
		if table.minKey != "m" {
			original := table.data
			table.data = []byte{0}
			defer func() { table.data = original }()
		}
	}
	rows, err := db.Scan("m", "n", 10)
	must(t, err)
	if !reflect.DeepEqual(rows, []Entry{{Key: "m", Value: "m"}}) {
		t.Fatal(rows)
	}
}
