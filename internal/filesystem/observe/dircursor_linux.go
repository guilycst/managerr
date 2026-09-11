//go:build linux

package observe

import (
	"bytes"
	"unsafe"

	"golang.org/x/sys/unix"
)

func parseDirectoryRecord(buf []byte) (directoryRecord, int, bool) {
	nameOffset := int(unsafe.Offsetof(unix.Dirent{}.Name))
	if len(buf) < nameOffset || len(buf) < int(unsafe.Offsetof(unix.Dirent{}.Reclen))+int(unsafe.Sizeof(unix.Dirent{}.Reclen)) {
		return directoryRecord{}, 0, false
	}
	record := (*unix.Dirent)(unsafe.Pointer(&buf[0]))
	consumed := int(record.Reclen)
	if consumed < nameOffset || consumed > len(buf) {
		return directoryRecord{}, 0, false
	}
	nameBytes := unsafe.Slice((*byte)(unsafe.Pointer(&record.Name[0])), len(record.Name))
	nameLength := consumed - nameOffset
	if nameLength > len(nameBytes) {
		nameLength = len(nameBytes)
	}
	nameBytes = nameBytes[:nameLength]
	if nul := bytes.IndexByte(nameBytes, 0); nul >= 0 {
		nameBytes = nameBytes[:nul]
	}
	return directoryRecord{Name: string(nameBytes), Valid: record.Ino != 0}, consumed, true
}
