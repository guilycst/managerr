//go:build darwin

package placement

import (
	"fmt"
	"os"
)

const placementWritesSupported = false

func createExclusiveChild(_ *os.File, _ string) (*os.File, error) {
	return nil, fmt.Errorf("%w: Darwin lacks descriptor-bound staging publication", ErrUnsupported)
}

// Darwin does not expose Linux's AT_EMPTY_PATH descriptor-link primitive.
// Darwin lacks a descriptor-bound hard-link primitive equivalent to Linux's
// AT_EMPTY_PATH. Refuse writes before any mutation rather than publishing a
// pathname that could be substituted after its identity check.
func linkStageNoReplace(_ *os.File, _ *os.File, _, _ string) error {
	return fmt.Errorf("%w: Darwin lacks descriptor-bound staging publication", ErrUnsupported)
}

func linkOpenSourceNoReplace(_ *os.File, _ *os.File, _ string) error {
	return fmt.Errorf("%w: Darwin lacks descriptor-bound hardlink publication", ErrUnsupported)
}
