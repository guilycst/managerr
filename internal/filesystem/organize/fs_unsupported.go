//go:build !linux

package organize

import (
	"context"
	"fmt"
	"os"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

// Non-Linux targets keep organize mutations fail-closed until reviewed
// descriptor-safe primitives exist for that target.
func moveOwned(context.Context, string, int, *nodeHandle, *os.File, string, ports.FileMap) error {
	return fmt.Errorf("%w: descriptor-bound organize move is unavailable on this platform", ErrUnsupported)
}

func deleteOwnedNode(context.Context, string, int, *os.File, string, domain.FileManifestEntry) ([]domain.FileManifestEntry, error) {
	return nil, fmt.Errorf("%w: descriptor-bound organize delete is unavailable on this platform", ErrUnsupported)
}

func linkDescriptorNoReplace(*os.File, *os.File, string) error {
	return fmt.Errorf("%w: descriptor-bound organize link is unavailable on this platform", ErrUnsupported)
}

func createPrivateDirectory(string, string) (*os.File, error) {
	return nil, fmt.Errorf("%w: private organize directory is unavailable on this platform", ErrUnsupported)
}

func makeDirectoryNoReplace(*os.File, string, os.FileMode) error {
	return fmt.Errorf("%w: descriptor-bound organize directory creation is unavailable on this platform", ErrUnsupported)
}

func createPrivateDirectoryAt(*os.File, string) (*os.File, error) {
	return nil, fmt.Errorf("%w: private organize directory is unavailable on this platform", ErrUnsupported)
}

func createFileNoReplace(*os.File, string, os.FileMode) (*os.File, error) {
	return nil, fmt.Errorf("%w: descriptor-bound organize file creation is unavailable on this platform", ErrUnsupported)
}
