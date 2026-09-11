//go:build darwin || linux

package observe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const directoryCursorBufferSize = 32 * 1024

type directoryCursor struct {
	file *os.File
	buf  []byte
	pos  int
	used int
	eof  bool
}

type directoryEntry struct {
	Name string
}

func newDirectoryCursor(file *os.File) *directoryCursor {
	return &directoryCursor{file: file, buf: make([]byte, directoryCursorBufferSize)}
}

func (cursor *directoryCursor) next(ctx context.Context) (directoryEntry, error) {
	for {
		if err := ctx.Err(); err != nil {
			return directoryEntry{}, err
		}
		if cursor.pos >= cursor.used {
			if cursor.eof {
				return directoryEntry{}, io.EOF
			}
			used, err := unix.ReadDirent(int(cursor.file.Fd()), cursor.buf)
			if err != nil {
				if errors.Is(err, unix.EINTR) {
					continue
				}
				return directoryEntry{}, fmt.Errorf("read directory stream: %w", err)
			}
			if used == 0 {
				cursor.eof = true
				return directoryEntry{}, io.EOF
			}
			cursor.used = used
			cursor.pos = 0
		}
		record, consumed, ok := parseDirectoryRecord(cursor.buf[cursor.pos:cursor.used])
		if !ok || consumed <= 0 {
			return directoryEntry{}, errors.New("malformed directory stream entry")
		}
		cursor.pos += consumed
		if !record.Valid || record.Name == "." || record.Name == ".." {
			continue
		}
		return directoryEntry{Name: record.Name}, nil
	}
}

type directoryRecord struct {
	Name  string
	Valid bool
}
