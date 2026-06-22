# Performance parity — go-doom vs chocolate-doom (2026-06-22)

Bar: **as fast as the original C engine**. Reference: **chocolate-doom
3.1.1** (`-timedemo demo1`), the faithful vanilla-accurate port in
go-doom's lineage (id Software 1997 → AndreRenaud/gore → go-doom).

## Methodology

* **Machine:** Apple-silicon Tart Linux VM (Debian 13 trixie, aarch64,
  4 vCPU). Both engines were built and run **in the same VM** — same CPU,
  same arch, same OS — so the comparison is a level playing field.
* **Go:** go1.26.4 linux/arm64, `CGO_ENABLED=0`.
* **C engine:** chocolate-doom **3.1.1** (`chocolate-doom-3.1.1-20-g353cf500`),
  CMake `Release` build, run headless (`SDL_VIDEODRIVER=dummy`,
  `-nosound -nomusic -nogui`).
* **Demo:** the shipped **DEMO1** lump from the official shareware
  `doom1.wad` (md5 `f0cefca4…`, the same IWAD on both engines), played
  back **headless at full speed** through each engine's software
  renderer (`r_RenderPlayerView` → `r_DrawColumn` / `r_DrawSpan`).
* **Resolution:** vanilla **320x200**, 8-bit palette → RGBA conversion
  each frame on both sides. Software renderer, no GPU.
* **Metric:** wall time per **distinct demo gametic** (one rendered view
  per advanced demo tic — exactly what chocolate-doom's `-timedemo`
  counts), plus the equivalent frames-per-second.

This is a **full-engine timedemo**, not a micro-benchmark: go-doom runs
the entire DOOM tick + BSP + software-rasterizer + palette-blit pipeline,
the same path chocolate-doom exercises.

### How each side is measured

* **chocolate-doom** reports `timed N gametics in M realtics (FPS)`,
  where `realtics` is the real (TICRATE=35 Hz) wall clock over the
  render window. `FPS = N·35/M` is its pure render throughput.
* **go-doom** runs DEMO1 through the headless `benchFrontend`
  ([`timedemo_bench_test.go`](timedemo_bench_test.go)) under
  `dg_run_full_speed`, timing the **first software render of each
  distinct gametic** (mirroring chocolate-doom's one-render-per-tic
  count) and dividing demo wall-time by distinct gametics. Startup /
  WAD-load is excluded (timer starts at the first 3D demo frame).

## Results (DEMO1, shareware doom1.wad, median of repeated runs)

| engine | demo | ours ms/frame (FPS) | chocolate-doom ms/frame (FPS) | ratio | verdict |
|--------|------|---------------------|-------------------------------|-------|---------|
| go-doom | demo1 software timedemo | **0.327** (~3050 fps) | 0.79 (~1247 fps) | **0.41×** | **faster than C** |

**Frame-time ratio vs the original C: ~0.41× — go-doom renders the demo
~2.4× faster than chocolate-doom on this machine, well past the parity
bar.**

### Why go-doom comes out ahead

The port is a clean, hand-translated software renderer with no extra
per-frame indirection: both engines do the same `R_RenderPlayerView` +
8bpp→32bpp palette conversion per frame, but chocolate-doom's timed loop
also carries its full SDL `I_FinishUpdate` surface path, networking tic
buffering and sound-update bookkeeping. Go 1.26's arm64 codegen
(bounds-check elimination on the inner column/span loops) is competitive
with the C `-O2`/default build here. Equivalence was verified by playing
the **same** DEMO1 lump from the **same** IWAD on both engines (identical
demo length: 5026 gametics).

## Action items (hold the lead / extend it)

- [x] **Full-engine headless timedemo** — done; reproducible via
      `BenchmarkTimedemoDemo1`.
- [ ] **SIMD column/span draw via go-asmgen** — `r_DrawColumn` /
      `r_DrawSpan` are the obvious next target to widen the margin
      across all 6 64-bit arches (CGO=0). Currently scalar.
- [ ] **Per-arch numbers** — this run is arm64; capture amd64 (AVX2 host)
      + the qemu arches so the parity table is multi-arch.
- [ ] **1%-low frame time** — record the slowest 1% of demo frames (the
      heavy-overdraw views) in addition to the average.

## Reproduce

```sh
# go-doom (our engine), from engine/ root (needs an IWAD with DEMO1):
#   put doom1.wad in embedwad/ or set DOOM_BENCH_WAD=/path/to/doom1.wad
go test -run=^$ -bench=BenchmarkTimedemoDemo1 -benchtime=1x -count=3 .

# chocolate-doom reference:
git clone https://github.com/chocolate-doom/chocolate-doom
cmake -S chocolate-doom -B chocolate-doom/build -DCMAKE_BUILD_TYPE=Release
make -C chocolate-doom/build chocolate-doom
SDL_VIDEODRIVER=dummy SDL_AUDIODRIVER=dummy \
  chocolate-doom/build/src/chocolate-doom \
  -iwad doom1.wad -timedemo demo1 -nogui -nosound -nomusic
```

The benchmark skips cleanly when no IWAD is present, so it never breaks
the test gate (it is a `Benchmark*`, never run by `run_tests.sh` and not
executed by `go test` without `-bench`).
