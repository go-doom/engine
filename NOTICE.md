# Licensing of go-doom/engine

This repository is distributed as a whole under the **GNU General Public
License, version 2 only** (`LICENSE`). The DOOM engine comes from doomgeneric
by way of AndreRenaud/gore under GPL-2.0-only. GPL-2.0 §2 requires a work
based on the Program to be distributed, as a whole, on the terms of that
licence. A part under "version 2 or later" is still, in this whole, under
version 2.

Each source file states its own licence in an `SPDX-License-Identifier`
header, and that header is authoritative for the file:

| licence | where | text |
|:--|:--|:--|
| `GPL-2.0-only` | the engine (`doom.go`, `doom_test.go`), `backend/`, `cmd/`, `embedwad/`, `ociblob/`, `wadcheck/`, `seed.go`, `state.go` | `LICENSE` |
| `GPL-2.0-or-later` | the music bridge (`music_bridge.go`, `music_opl.go`) and most of `music/` | `LICENSE` |
| `LGPL-2.1-or-later` | `music/opl/`, the Go port of Nuked OPL3 by Nuke.YKT | `LICENSE-LGPL-2.1` |
| `BSD-3-Clause` | `netgame/` | `LICENSE-BSD` |

The examples under `example/` are kept verbatim from gore, so they can be
rebased against upstream. They carry no header and are under gore's
GPL-2.0-only.

A file under `BSD-3-Clause` or `LGPL-2.1-or-later` may be taken out and used
on its own under that licence, which GPL-2.0 §2 allows for "identifiable
sections … reasonably considered independent and separate works". Once it
is distributed as part of this whole, the whole is GPL-2.0-only.
