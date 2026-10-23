//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"syscall"
)

func mapFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 {
		return nil, nil
	}
	if st.Size() > int64(int(^uint(0)>>1)) {
		return nil, errors.New("file exceeds address space")
	}
	return syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
}
func (t *sstable) mapFiles() error {
	var err error
	t.data, err = mapFile(t.path)
	if err != nil {
		return err
	}
	t.indexData, err = mapFile(t.indexPath)
	if err != nil {
		t.close()
		return err
	}
	return nil
}
func (t *sstable) close() error {
	var err error
	if t.data != nil {
		err = syscall.Munmap(t.data)
		t.data = nil
	}
	if t.indexData != nil {
		if e := syscall.Munmap(t.indexData); err == nil {
			err = e
		}
		t.indexData = nil
	}
	return err
}

// recordAt validates bounds before touching a length-prefixed payload.
func recordAt(data []byte, offset int64) (Entry, int64, error) {
	if offset < 0 || offset > int64(len(data)) {
		return Entry{}, 0, errors.New("invalid record offset")
	}
	if offset == int64(len(data)) {
		return Entry{}, 0, io.EOF
	}
	b := data[offset:]
	if b[0] != 'K' {
		end := bytes.IndexByte(b, '\n')
		if end < 0 {
			return Entry{}, 0, io.ErrUnexpectedEOF
		}
		line := b[:end]
		var e Entry
		if bytes.HasPrefix(line, []byte("DELETE ")) {
			e = Entry{Key: string(line[7:]), Deleted: true}
		} else if bytes.HasPrefix(line, []byte("PUT ")) {
			split := bytes.IndexByte(line[4:], ' ')
			if split < 0 {
				return Entry{}, 0, errors.New("invalid legacy record")
			}
			e = Entry{Key: string(line[4 : 4+split]), Value: string(line[5+split:])}
		} else {
			return Entry{}, 0, errors.New("invalid legacy record")
		}
		return e, int64(end + 1), nil
	}
	if len(b) < recordHeader {
		return Entry{}, 0, io.ErrUnexpectedEOF
	}
	if string(b[:4]) != "KVR1" || b[4] > 1 {
		return Entry{}, 0, errors.New("invalid binary record header")
	}
	kl, vl := uint64(binary.LittleEndian.Uint32(b[5:9])), uint64(binary.LittleEndian.Uint32(b[9:13]))
	size := uint64(recordHeader) + kl + vl
	if kl+vl > maxRecordSize {
		return Entry{}, 0, errors.New("record length exceeds limit")
	}
	if size > uint64(len(b)) {
		return Entry{}, 0, io.ErrUnexpectedEOF
	}
	payload := b[recordHeader:size]
	if recordCRC(b, payload) != binary.LittleEndian.Uint32(b[13:17]) {
		return Entry{}, 0, errors.New("record checksum mismatch")
	}
	return Entry{Key: string(payload[:kl]), Value: string(payload[kl:]), Deleted: b[4] == 1}, int64(size), nil
}
