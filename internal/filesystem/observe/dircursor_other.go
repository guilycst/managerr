//go:build !(darwin || linux)

package observe

import (
	"context"
	"errors"
	"io"
	"os"
)

// Non-Linux/Darwin platforms keep bounded direct-child reads portable. The
// observer retains this descriptor-backed stream in its capped cursor store,
// so continuation still has no replay gap without relying on platform cookies.
type directoryCursor struct {
	file *os.File
}

type directoryEntry struct {
	Name string
}

func newDirectoryCursor(file *os.File) *directoryCursor {
	return &directoryCursor{file: file}
}

func (cursor *directoryCursor) next(ctx context.Context) (directoryEntry, error) {
	if err := ctx.Err(); err != nil {
		return directoryEntry{}, err
	}
	names, err := cursor.file.Readdirnames(1)
	if len(names) > 0 {
		return directoryEntry{Name: names[0]}, nil
	}
	if errors.Is(err, io.EOF) {
		return directoryEntry{}, io.EOF
	}
	return directoryEntry{}, err
}
