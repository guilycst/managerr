package organize

import "errors"

// errCrossDevice keeps the public error stable while preserving the native
// EXDEV cause inside platform implementations.
var errCrossDevice = errors.New("filesystem organize native cross-device error")
