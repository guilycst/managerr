//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package observe

import (
	"fmt"
	"io/fs"
	"syscall"
)

func fileIdentity(info fs.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("unix:dev=%d:ino=%d:size=%d:mtime=%d", stat.Dev, stat.Ino, info.Size(), info.ModTime().UnixNano())
	}
	return fmt.Sprintf("unix:mode=%o:size=%d:mtime=%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
}
