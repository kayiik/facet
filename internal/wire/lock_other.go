//go:build !windows && !unix

package wire

import "time"

// lockFile is a no-op where Compa does not run.
func lockFile(string, time.Duration) (func(), error) { return func() {}, nil }
