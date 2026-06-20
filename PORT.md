# PORT.md -- godoom adaptation plan for cloud-boot TamaGo + UEFI

This document is the engineering contract between `go-doom/engine` and the
sibling sprints producing `go-virtio/gpu`, `go-virtio/sound`,
`go-virtio/input`, and the `cloud-boot/tamago-uefi` "livedoom" boot artifact.
It is the source of truth for *what* godoom needs to do and *what shape* its
dependencies must take.

## 0. Premises (verified at sprint start)

- Engine: a fork of `AndreRenaud/gore`, which is a pure-Go transpilation of
  `doomgeneric`. ~990 KB of generated Go, single package `gore`.
- CGO: **never**. Confirmed builds with `CGO_ENABLED=0` for
  `darwin/arm64`, `linux/amd64`, `linux/arm64` (i.e. the live TamaGo targets).
- Frontend contract: the engine consumes one interface,
  `gore.DoomFrontend`:

  ```go
  type DoomFrontend interface {
      DrawFrame(img *image.RGBA)
      SetTitle(title string)
      GetEvent(event *DoomEvent) bool
      CacheSound(name string, data []byte)
      PlaySound(name string, channel, vol, sep int)
  }
  ```

  This is the entire surface we need to drive virtio devices.

- WAD ingestion: the engine reads its IWAD via `fs.FS`, settable through
  `gore.SetVirtualFileSystem`. We do not need a real filesystem on the
  bare-metal target.

- Memory budget: TamaGo amd64 ships with a 256 MiB heap. DOOM's working
  set is ~32 MiB plus a 4 MiB shareware WAD (or 12 MiB DOOM2). Comfortable
  with a 4x margin.

## 1. Rendering: DOOM -> virtio-gpu framebuffer

### Engine contract
- DrawFrame is called once per game tick with an `*image.RGBA` of exactly
  `SCREENWIDTH x SCREENHEIGHT` (320x200) RGBA8888.

### Adaptation
1. `backend/tamago` exposes a `GPU` interface with `Flip(*image.RGBA) error`.
2. The real `go-virtio/gpu` driver will:
   - Allocate a host-visible 2D resource of size 320x200 (or a larger
     scanout with integer scale, decided by the driver).
   - Map the engine's RGBA backing slice as the transfer source.
   - Submit `RESOURCE_FLUSH` + `SET_SCANOUT` on each Flip.
3. We deliberately do not impose a vsync model in the interface; the
   driver may block or be wait-free. The engine has its own 35 Hz tick.
4. Letter-boxing and aspect correction (4:3 vs 320x200) is the driver's
   problem, not godoom's.

### Open questions for go-virtio/gpu
- Does Flip take ownership of the pixel buffer (no), or is it a borrow
  (yes, current assumption)? -- documented in `frontend.go` GoDoc.
- Is there a cursor / overlay plane needed for the menu? -- no, DOOM
  draws its own cursor.

## 2. Audio: DOOM SFX -> virtio-sound

### Engine contract
- CacheSound is called once per `dmx` lump with the full lump bytes
  (8-byte header + 8-bit unsigned PCM @ 11025 Hz mono).
- PlaySound is called per game event with (name, channel, vol[0..127],
  sep[0..255]). The engine assumes mixing happens host-side.

### Adaptation
1. `backend/tamago` exposes a `Sound` interface
   `{ Cache(name, data) error; Play(name, ch, vol, sep) error }`.
2. The real `go-virtio/sound` driver will:
   - Open one PCM output stream (CONFIG_PCM_OUTPUT) at 11025 Hz, mono,
     u8. If the host advertises only s16le, we resample/convert in the
     driver, not in godoom.
   - Maintain an in-driver SFX dictionary keyed by name; Cache copies the
     PCM payload (skipping the 8-byte dmx header) into a pinned buffer.
   - Play schedules mixing of the named buffer into the stream at the
     given volume / panning. We expect the driver to support at least
     8 voices to match DOOM's channel count.

### Out of scope for sprint 1
- MUS / MIDI music. The engine emits MUS events to a `sound_module`
  interface we currently leave nil. Sprint 2 will add a tiny soundfont
  synth (likely a port of `go-soundfont` used by
  `danielgatis/go-doom`) routed through the same virtio-sound stream.

## 3. Input: keyboard -> virtio-input

### Engine contract
- GetEvent fills a `gore.DoomEvent {Type, Key, Mouse}`. Returns true to
  consume an event, false to stop draining for this tick.
- Mouse is optional; sprint 1 keyboard-only.

### Adaptation
1. `backend/tamago` exposes an `Input` interface returning raw HID
   keyboard events `{HIDUsage uint16, Down bool}`.
2. Translation table from HID usage IDs to DOOM scancodes lives in
   `frontend.go::hidUsageToDoomKey`. Initial coverage: arrows, enter,
   escape, space (use), tab (automap), Ctrl (fire).
3. Auto-repeat: handled by the input device (it stops generating events
   after the HID auto-key-repeat period). godoom does NOT need to
   synthesize a release-after-N-ticks the way `termdoom` does, because
   we have real key-up events from HID.

### Open questions for go-virtio/input
- Does the driver coalesce events or hand us one per Poll? Either works
  but the GoDoc on `Input.Poll` must specify.

## 4. WAD ingestion: embed vs OCI stream

### Phase 1 -- embed (sprint 1, planned)
The cloud-boot "livedoom" artifact does:

```go
//go:embed doom1.wad
var doom1 []byte

func main() {
    gore.SetVirtualFileSystem(embedwad.New("doom1.wad", doom1))
    fe := tamago.New(gpu, snd, in)
    gore.Run(fe, []string{"-iwad", "doom1.wad"})
}
```

This works on day one with zero IO machinery.

### Phase 2 -- OCI artifact stream (sprint follow-up)
- The bootloader pulls the WAD as an OCI artifact layer and exposes the
  bytes to the kernel image at a fixed memory address.
- "livedoom" reads from that address into a `[]byte` slice, then wraps it
  in `embedwad.New(...)`. Same fs.FS shim, different backing.
- No change required in godoom itself.

## 5. TamaGo runtime constraints

| Constraint                          | Effect on godoom                              |
|-------------------------------------|-----------------------------------------------|
| No signal handling                  | The engine never installs handlers; ok.       |
| No `syscall` package                | doom.go uses only `os.DirFS` + std lib; `SetVirtualFileSystem` lets us replace it before any IO. |
| No real preemptive scheduler        | The engine runs in a single goroutine via `Run`; no fan-out. ok. |
| `time.Sleep` uses runtime timers    | TamaGo provides these. We do NOT use busyWait. |
| 256 MiB heap on amd64               | DOOM 32 MiB working set + 4 MiB WAD = ~36 MiB. 4x margin. |
| No filesystem                       | All IO goes through the `fs.FS` shim.         |
| No CGO                              | gore is pure Go; SDL/Ebitengine examples are excluded from livedoom builds via `go build .` of `livedoom/` only. |

## 6. Migration plan (sprints)

| Sprint | Work item                                                       | Status   |
|-------:|-----------------------------------------------------------------|----------|
| 1A     | Survey + fork + scaffold + smoke test                            | DONE (this sprint) |
| 1B     | Land `go-virtio/sound` driver matching `Sound` interface         | parallel sprint |
| 1C     | Land `go-virtio/input` driver matching `Input` interface         | parallel sprint |
| 2A     | Wire `livedoom/` boot artifact in cloud-boot/tamago-uefi          | next     |
| 2B     | Live demo run on bare-metal UEFI VM, capture video               | next     |
| 2C     | MUS -> MIDI -> soundfont music path                               | optional |

## 7. Non-goals (this repository, ever)

- Network multiplayer.
- Mod / PWAD support beyond what the engine already gives us
  (the engine reads multiple WADs via `-file`; no extra work needed but
  also not a sprint focus).
- Saving / loading game state across reboots (no persistent storage in
  the demo target).
- Rewriting `doom.go`. The transpiled blob stays as-is. Improvements
  upstream in `gore` will be rebased in periodically.

## 8. Open questions tracked upstream

- gore #N (TBD): `unsafe` usage in scanline blitter -- safe under TamaGo?
- gore #N (TBD): exported `KEY_*` constants are upper-case ALL CAPS
  because of the transpiler; this is harmless for our use.
- gore #N (TBD): "Run called twice, ignoring second call" warns at
  WARN level; we should make this an error since TamaGo will only call
  Run once per boot.
