// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

//go:build !(js && wasm)

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "doom-wasmbox is a wasmbox client: build it with GOOS=js GOARCH=wasm and launch it from the wasmbox desktop")
	os.Exit(2)
}
