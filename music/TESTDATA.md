# Test-data provenance and licensing

The music packages ship small binary fixtures under `*/testdata/`. Their
provenance and license:

| File(s)                                   | Origin                                                                 | License |
|-------------------------------------------|------------------------------------------------------------------------|---------|
| `mus/testdata/*.mus`                       | Hand-authored synthetic MUS vectors (this project)                     | BSD-3-Clause |
| `mus/testdata/*.mid`                       | Mechanical output of chocolate-doom `mus2mid.c` on the `.mus` vectors (the differential oracle) | derived / functional |
| `opl/testdata/*.pcm`                       | PCM captured from the Nuked OPL3 C emulator over a synthetic register script | derived / functional |
| `genmidi/testdata/GENMIDI.lmp`             | `GENMIDI` lump from **Freedoom Phase 1** (`freedoom1.wad`)             | BSD-3-Clause equivalent (Freedoom) |
| `midi/testdata/D_INTRO.lmp`                | `D_INTRO` music lump from **Freedoom Phase 1** (`freedoom1.wad`)       | BSD-3-Clause equivalent (Freedoom) |
| `oplplayer/testdata/regtrace_dintro.txt`   | OPL register-write trace of chocolate-doom `i_oplmusic.c` playing the Freedoom `D_INTRO` (the differential oracle) | derived / functional |

The `GENMIDI.lmp` and `D_INTRO.lmp` fixtures come from
[Freedoom](https://freedoom.github.io/), a free/libre content replacement
released under a BSD-3-Clause-equivalent license, so they are freely
redistributable. No copyrighted id Software WAD data is committed; the engine's
own WAD blob remains git-ignored (see `embedwad/.gitignore`).
