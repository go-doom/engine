// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (c) the go-doom/engine authors.
//
// music_bridge.go wires the pure-Go OPL music path into the transpiled engine.
// The engine's initMusicModule() (in doom.go) calls installMusicModule when it
// is non-nil; music_opl.go sets it to install an OPL2/OPL3 synth driving the
// DMX GENMIDI bank (the chocolate-doom OPL path), keeping the synth entirely
// out of the generated blob.

package gore

// installMusicModule, when non-nil, is invoked by initMusicModule() to install
// the active music_module. It is set from music_opl.go's init().
var installMusicModule func()
