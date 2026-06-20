<p align="center"><img src="https://raw.githubusercontent.com/cloud-boot/brand/main/social/cloud-boot.png" alt="go-doom/engine" width="720"></p>

# go-doom/engine

A pure-Go DOOM engine adapted for cloud-boot's bare-metal TamaGo + UEFI demo
target.

## Fork origin

This repository is a fork of [AndreRenaud/gore](https://github.com/AndreRenaud/gore)
at commit `7dc6c65493b8a29e0b93dd00dac21a2f10a0068a` (2026-05-11).

`gore` itself is a Go transpilation of [doomgeneric](https://github.com/ozkl/doomgeneric)
performed with [`modernc.org/ccgo/v4`](https://gitlab.com/cznic/doomgeneric.git)
and then hand-cleaned. The engine has no CGO dependency, only standard
library, and exposes its host bindings through a small `DoomFrontend`
interface -- exactly the shape we need to wire DOOM into cloud-boot's
virtio device tree.

The upstream `README.md` is preserved as `README.upstream.md`.

## Why this fork exists

cloud-boot is building an OS-agnostic OCI boot architecture on top of
TamaGo + UEFI. To showcase Phase 3 we want to run classic DOOM as the
"OS payload", with rendering, sound, and input wired straight to virtio
devices instead of SDL / X11 / a kernel.

This fork therefore adds:

- `backend/tamago/` -- a new `DoomFrontend` implementation that drives
  cloud-boot's virtio-gpu, virtio-sound, and virtio-input drivers
  (currently stub interfaces; real wiring lands once
  [`go-virtio/sound`](https://github.com/go-virtio/sound) and
  [`go-virtio/input`](https://github.com/go-virtio/input) ship in their
  sibling sprints).
- `internal/embedwad/` -- an `io/fs.FS` shim that serves a WAD blob from
  memory, so the engine can load the IWAD without a real filesystem.

The upstream `gore` engine (`doom.go`, ~990 KB of transpiled code, plus
`doom_test.go`) and its existing terminal / web / Ebitengine / SDL
examples are kept verbatim, so we can rebase against upstream cleanly.

## License boundary

- The DOOM engine (`doom.go`, `doom_test.go`, `LICENSE`) inherits
  **GPL-2.0-only** from `doomgeneric` / `gore`. All cloud-boot additions
  in this repository (`backend/tamago`, `internal/embedwad`) are released
  under the same license, by necessity (linking + derived work).
- The rest of the cloud-boot stack is **BSD-3-Clause** and is *not*
  affected: cloud-boot's bootloader, runtime, and host components do not
  import this package. The integration happens via a thin separate
  "livedoom" boot artifact whose only job is to call `godoom.Run`.
  That artifact will itself be GPL-2.0, isolated to a single directory in
  the cloud-boot/tamago-uefi tree.

## Layout

```
.
|-- doom.go                     # upstream gore engine (transpiled DOOM)
|-- doom_test.go                # upstream gore reference-frame tests
|-- example/                    # upstream gore examples (termdoom, web, ...)
|-- backend/
|   `-- tamago/                 # NEW: DoomFrontend over virtio-gpu/sound/input
|-- internal/
|   `-- embedwad/               # NEW: io/fs.FS shim over an in-memory WAD
|-- PORT.md                     # Adaptation plan (read this)
`-- README.md
```

## Status

| Aspect                            | Status                                    |
|-----------------------------------|-------------------------------------------|
| Pure Go (CGO=0)                   | yes (engine + all new code)               |
| Builds on Go 1.26.4               | yes (lib + tamago backend + pure-Go examples) |
| Cross-compiles linux/amd64        | yes (CGO=0)                               |
| Cross-compiles linux/arm64        | yes (CGO=0)                               |
| Runs shareware DOOM1.WAD          | yes (engine ticks; verified via TestMenus harness) |
| TamaGo backend wired              | scaffold only; real drivers land in follow-up sprint |
| Music (MUS -> MIDI)               | out of scope for sprint 1                 |
| Multiplayer                       | out of scope                              |

## Quick smoke test (host)

```bash
# 1. fetch the shareware IWAD (free re-release):
curl -sSL -o doom1.wad https://distro.ibiblio.org/slitaz/sources/packages/d/doom1.wad

# 2. terminal renderer (pure Go):
CGO_ENABLED=0 go run ./example/termdoom -iwad doom1.wad
```

## cloud-boot integration points

The TamaGo backend in `backend/tamago/` consumes three driver
interfaces declared in that package:

| Frontend method          | Backend interface | Driver repo (sibling) |
|--------------------------|-------------------|-----------------------|
| `DrawFrame(*image.RGBA)` | `GPU.Flip`        | `github.com/go-virtio/gpu`   |
| `CacheSound / PlaySound` | `Sound.Cache/Play`| `github.com/go-virtio/sound` |
| `GetEvent(*DoomEvent)`   | `Input.Poll`      | `github.com/go-virtio/input` |

The WAD is delivered to the engine via `gore.SetVirtualFileSystem(fs.FS)`
using the `internal/embedwad` helper, whose backing bytes can come from
either (a) a `go:embed` blob in the cloud-boot "livedoom" boot artifact,
or (b) a runtime stream from a cloud-boot OCI artifact mount.

The actual wire-in (`phase3_oci_doom_boot.go` in cloud-boot/tamago-uefi)
is a follow-up sprint and is intentionally NOT done here.

See `PORT.md` for the detailed adaptation plan.

## How to verify the cloud-boot DOOM demo is operational (R-doom1g)

The provable test protocol lives in
[`cloud-boot/docs/docs/architecture/doom-provable-protocol.md`](https://github.com/cloud-boot/docs/blob/main/docs/architecture/doom-provable-protocol.md).
Short version:

```bash
# Re-harvest the host oracle (byte-equal reproducible — proves
# engine + WAD + determinism hooks are intact):
cd go-doom/engine
GOWORK=off go run ./cmd/harvest-reference \
    -wad embedwad/doom1.wad \
    -seed 0 \
    -checkpoints 1,35,70,140,350,1050 \
    -out /tmp/fresh-oracle

# Compare against the committed oracle — every SHA must match.
diff <(cd oracle && sha256sum frame_tic*.ppm) \
     <(cd /tmp/fresh-oracle && sha256sum frame_tic*.ppm)
```

For the full guest-stack gate (boots the EFI probe in QEMU+EDK2 and
compares virtio-gpu output against the guest oracle):

```bash
cd cloud-boot/tamago-uefi
task doomboot:efi:amd64
bash internal/livedoomboot/provable_test.sh amd64
```

The `oracle/` directory is committed (~1.1 MiB of PPMs + manifest).
The WAD itself is `.gitignore`d (see `embedwad/.gitignore`). The
determinism hooks live in `seed.go` (sibling of `doom.go`, never
modifying the transpiled engine source).
