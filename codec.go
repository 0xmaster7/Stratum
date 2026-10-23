package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"strings"
)

const recordHeader = 17
const maxRecordSize = 64 << 20

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func recordCRC(header, payload []byte) uint32 {
	sum := crc32.Update(0, crcTable, header[4:13])
	return crc32.Update(sum, crcTable, payload)
}
func writeRecord(w io.Writer, e Entry) (int64, error) {
	if len(e.Key)+len(e.Value) > maxRecordSize {
		return 0, errors.New("record exceeds 64 MiB limit")
	}
	b := make([]byte, recordHeader+len(e.Key)+len(e.Value))
	copy(b, "KVR1")
	if e.Deleted {
		b[4] = 1
	}
	binary.LittleEndian.PutUint32(b[5:9], uint32(len(e.Key)))
	binary.LittleEndian.PutUint32(b[9:13], uint32(len(e.Value)))
	copy(b[recordHeader:], e.Key)
	copy(b[recordHeader+len(e.Key):], e.Value)
	binary.LittleEndian.PutUint32(b[13:17], recordCRC(b, b[recordHeader:]))
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return int64(n), err
}
func readRecord(r *bufio.Reader) (Entry, int64, error) {
	prefix, err := r.Peek(1)
	if err != nil {
		return Entry{}, 0, err
	}
	if prefix[0] != 'K' {
		return readLegacyRecord(r)
	}
	var header [recordHeader]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Entry{}, 0, err
	}
	if string(header[:4]) != "KVR1" || header[4] > 1 {
		return Entry{}, 0, errors.New("invalid binary record header")
	}
	kl, vl := uint64(binary.LittleEndian.Uint32(header[5:9])), uint64(binary.LittleEndian.Uint32(header[9:13]))
	if kl+vl > maxRecordSize {
		return Entry{}, 0, errors.New("record length exceeds limit")
	}
	b := make([]byte, int(kl+vl))
	if _, err = io.ReadFull(r, b); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Entry{}, 0, err
	}
	if recordCRC(header[:], b) != binary.LittleEndian.Uint32(header[13:17]) {
		return Entry{}, 0, errors.New("record checksum mismatch")
	}
	return Entry{Key: string(b[:kl]), Value: string(b[kl:]), Deleted: header[4] == 1}, int64(recordHeader + len(b)), nil
}
func readLegacyRecord(r *bufio.Reader) (Entry, int64, error) {
	s, err := r.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(s) > 0 {
			err = io.ErrUnexpectedEOF
		}
		return Entry{}, 0, err
	}
	p := strings.SplitN(strings.TrimSuffix(s, "\n"), " ", 3)
	if len(p) == 2 && p[0] == "DELETE" {
		return Entry{Key: p[1], Deleted: true}, int64(len(s)), nil
	}
	if len(p) == 3 && p[0] == "PUT" {
		return Entry{Key: p[1], Value: p[2]}, int64(len(s)), nil
	}
	return Entry{}, 0, errors.New("invalid text record")
}
func writeIndex(w io.Writer, key string, offset int64) (int64, error) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(offset))
	return writeRecord(w, Entry{Key: key, Value: string(b[:])})
}
