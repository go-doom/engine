// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package wasmbox

// Mixer sums the engine's sound effects into an interleaved stereo float32
// stream at a fixed output rate. The wasmbox relay carries PCM, not
// "play this buffer" events, so the effects the engine requests one at a time
// (PlaySound) have to be mixed here and written into the ring next to the
// music. One voice per engine channel: a new sound on a busy channel replaces
// the old one, as the engine's channel model (and chocolate-doom) does.
type Mixer struct {
	rate   float64
	voices map[int]*voice
}

type voice struct {
	s    []float32
	pos  float64
	step float64 // source samples per output frame
	l, r float32
}

// NewMixer returns a mixer producing frames at rate Hz.
func NewMixer(rate int) *Mixer {
	return &Mixer{rate: float64(rate), voices: map[int]*voice{}}
}

// Play starts sound s on engine channel ch with the engine's volume (0..127)
// and separation (0..254), panned by Pan.
func (m *Mixer) Play(ch int, s Sound, vol, sep int) {
	if len(s.Samples) == 0 || s.Rate <= 0 {
		return
	}
	l, r := Pan(vol, sep)
	m.voices[ch] = &voice{s: s.Samples, step: float64(s.Rate) / m.rate, l: l, r: r}
}

// Active is the number of voices still playing.
func (m *Mixer) Active() int { return len(m.voices) }

// Mix adds the active voices into dst (interleaved stereo; a trailing odd
// sample is left alone), linearly resampling each, then clamps the sum to
// [-1, 1] so music plus effects cannot exceed full scale. Finished voices are
// dropped.
func (m *Mixer) Mix(dst []float32) {
	frames := len(dst) / 2
	for ch, v := range m.voices {
		n := len(v.s)
		for f := 0; f < frames; f++ {
			i := int(v.pos)
			if i >= n {
				break
			}
			a := v.s[i]
			b := a
			if i+1 < n {
				b = v.s[i+1]
			}
			x := a + (b-a)*float32(v.pos-float64(i))
			dst[2*f] += x * v.l
			dst[2*f+1] += x * v.r
			v.pos += v.step
		}
		if int(v.pos) >= n {
			delete(m.voices, ch)
		}
	}
	for i := 0; i < 2*frames; i++ {
		if dst[i] > 1 {
			dst[i] = 1
		} else if dst[i] < -1 {
			dst[i] = -1
		}
	}
}
