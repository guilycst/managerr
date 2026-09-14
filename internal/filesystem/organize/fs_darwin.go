//go:build darwin

package organize

import (
	"fmt"
	"os"
)

const organizeWritesSupported = false

func renameNoReplace(_ *os.File, _ string, _ *os.File, _ string) (bool, error) {
	return false, fmt.Errorf("%w: Darwin lacks reviewed descriptor-safe no-replace organize publication", ErrUnsupported)
}

func removeEntry(_ *os.File, _ string, _ bool) error {
	return fmt.Errorf("%w: Darwin lacks reviewed organize deletion primitive", ErrUnsupported)
}
