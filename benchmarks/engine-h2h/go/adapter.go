//go:build enginebench

package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/binary"
	"encoding/json"
	"runtime"
	"runtime/cgo"
	"unsafe"
)

func outputError(out **C.char, err error) C.int {
	if err == nil {
		return 0
	}
	*out = C.CString(err.Error())
	return -1
}

var activeDatabase *DB

func database(handle C.ulonglong) *DB { return cgo.Handle(handle).Value().(*DB) }

//export GolsmOpen
func GolsmOpen(path *C.char, durable C.int, threshold C.int, errOut **C.char) C.ulonglong {
	db, err := Open(C.GoString(path), Options{SyncWrites: durable != 0, FlushThreshold: int(threshold)})
	if err != nil {
		outputError(errOut, err)
		return 0
	}
	activeDatabase = db
	return C.ulonglong(cgo.NewHandle(db))
}

//export GolsmClose
func GolsmClose(handle C.ulonglong, errOut **C.char) C.int {
	h := cgo.Handle(handle)
	err := h.Value().(*DB).Close()
	h.Delete()
	activeDatabase = nil
	return outputError(errOut, err)
}

//export GolsmPut
func GolsmPut(handle C.ulonglong, key unsafe.Pointer, keyLen C.longlong, value unsafe.Pointer, valueLen C.longlong, errOut **C.char) C.int {
	return outputError(errOut, database(handle).Put(C.GoStringN((*C.char)(key), C.int(keyLen)), C.GoStringN((*C.char)(value), C.int(valueLen))))
}

//export GolsmGet
func GolsmGet(handle C.ulonglong, key unsafe.Pointer, keyLen C.longlong, out *unsafe.Pointer, outLen *C.longlong, errOut **C.char) C.int {
	v, ok, err := database(handle).Get(C.GoStringN((*C.char)(key), C.int(keyLen)))
	if err != nil {
		return outputError(errOut, err)
	}
	if !ok {
		return 1
	}
	*out = C.CBytes([]byte(v))
	*outLen = C.longlong(len(v))
	return 0
}

//export GolsmScan
func GolsmScan(handle C.ulonglong, start *C.char, end *C.char, limit C.int, out *unsafe.Pointer, outLen *C.longlong, errOut **C.char) C.int {
	rows, err := database(handle).Scan(C.GoString(start), C.GoString(end), int(limit))
	if err != nil {
		return outputError(errOut, err)
	}
	size := 0
	for _, r := range rows {
		size += 4 + len(r.Value)
	}
	b := make([]byte, 0, size)
	for _, r := range rows {
		b = binary.LittleEndian.AppendUint32(b, uint32(len(r.Value)))
		b = append(b, r.Value...)
	}
	*out = C.CBytes(b)
	*outLen = C.longlong(len(b))
	return 0
}

//export GolsmFinish
func GolsmFinish(handle C.ulonglong, errOut **C.char) C.int {
	return outputError(errOut, database(handle).Flush())
}

//export GolsmFree
func GolsmFree(p unsafe.Pointer) { C.free(p) }

//export GolsmNoop
func GolsmNoop() C.int { return 1 }

//export GolsmEcho
func GolsmEcho(data unsafe.Pointer, length C.longlong) unsafe.Pointer {
	return C.CBytes(unsafe.Slice((*byte)(data), int(length)))
}

//export GolsmState
func GolsmState(out *unsafe.Pointer, outLen *C.longlong) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	b, err := json.Marshal(map[string]any{"metrics": activeDatabase.Stats(), "go_heap_alloc": mem.HeapAlloc, "go_num_gc": mem.NumGC, "gomaxprocs": runtime.GOMAXPROCS(0)})
	if err != nil {
		panic(err)
	}
	*out = C.CBytes(b)
	*outLen = C.longlong(len(b))
}
func main() {}
