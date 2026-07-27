// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (c) the go-doom/engine authors.
//
// Resolve testdata regardless of the working directory. Native `go test` runs
// with cwd = this package dir, but the 6-arch QEMU CI harness runs the
// `go test -c` binary from the module root, so relative "testdata/..." paths
// would not resolve there (and the module root has its own unrelated testdata/
// dir). Locate the module root (the go.mod ancestor) and chdir into this
// package's directory, so every relative fixture read works on all arches.

package midi

import (
	"os"
	"path/filepath"
)

func init() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			_ = os.Chdir(filepath.Join(dir, "music/midi"))
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return // no go.mod found; leave cwd as-is
		}
		dir = parent
	}
}
