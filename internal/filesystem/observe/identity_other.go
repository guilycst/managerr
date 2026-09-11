//go:build !(darwin || linux)

package observe

import (
	"fmt"
	"io/fs"
)

func fileIdentity(info fs.FileInfo) string {
	// This fallback is intentionally weaker than Unix device/inode identity.
	// Capabilities report no-follow as unknown on these platforms.
	return fmt.Sprintf("fallback:mode=%o:size=%d:mtime=%d:name=%s", info.Mode(), info.Size(), info.ModTime().UnixNano(), info.Name())
}
