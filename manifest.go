package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type manifest struct {
	Tables []int `json:"tables"`
}

func readManifest(dir string) (map[int]bool, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, "MANIFEST"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var m manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, false, err
	}
	ids := make(map[int]bool)
	for _, id := range m.Tables {
		if id < 1 || ids[id] {
			return nil, false, errors.New("invalid manifest table ID")
		}
		ids[id] = true
	}
	return ids, true, nil
}

// The manifest is the commit point for a flush or compaction. Readers only use
// files named by it; files left by interrupted publication are never resurrected.
func (db *DB) publish(tables []*sstable) error {
	m := manifest{Tables: make([]int, 0, len(tables))}
	for _, t := range tables {
		m.Tables = append(m.Tables, t.id)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	path := filepath.Join(db.dir, "MANIFEST")
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer os.Remove(path + ".tmp")
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return err
	}
	return syncDir(db.dir)
}
func (db *DB) install(tables []*sstable) error {
	if err := db.publish(tables); err != nil {
		db.writeErr = err
		return err
	}
	db.tables = tables
	return nil
}
