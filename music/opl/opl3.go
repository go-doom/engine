// SPDX-License-Identifier: LGPL-2.1-or-later
// Go port of Nuked OPL3 (github.com/nukeykt/Nuked-OPL3) by Nuke.YKT.
// Ported for the go-doom/engine authors. Original C: LGPL-2.1-or-later.
//
// This is a faithful, bit-exact port of the Nuked-OPL3-fast fork
// (https://github.com/tgies/Nuked-OPL3-fast, upstream commit cfedb09,
// fork 1.8-fast.1) of the Yamaha YMF262 (OPL3) FM synthesis chip as
// shipped in chocolate-doom's OPL music backend. Audio output is
// identical to the C reference for the same register stream.
//
// The port keeps the same integer widths (uint8/int16/uint16/uint32/
// int32/uint64) and the same wraparound arithmetic as the C source, and
// reproduces the pointer aliasing between operator slots and channels
// using Go pointers into the (never-moved) Chip struct.
//
// The stereo-extension and 4-channel (OPL_ENABLE_STEREOEXT) paths of the
// upstream source are compiled out in the chocolate-doom configuration
// (OPL_ENABLE_STEREOEXT == 0, OPL_QUIRK_CHANNELSAMPLEDELAY == 1); this
// port implements that exact configuration.

package opl

import "math/bits"

// Compile-time configuration matching chocolate-doom's opl3.c:
//   OPL_ENABLE_STEREOEXT        == 0
//   OPL_QUIRK_CHANNELSAMPLEDELAY == 1

const (
	writebufSize  = 1024
	writebufDelay = 2
	rsmFrac       = 10
)

// Channel types.
const (
	ch2op  = 0
	ch4op  = 1
	ch4op2 = 2
	chDrum = 3
)

// Envelope key types.
const (
	egkNorm = 0x01
	egkDrum = 0x02
)

// Envelope generator phases.
const (
	envAttack  = 0
	envDecay   = 1
	envSustain = 2
	envRelease = 3
)

// exp table (verbatim from opl3.c).
var exprom = [256]uint16{
	0xff4, 0xfea, 0xfde, 0xfd4, 0xfc8, 0xfbe, 0xfb4, 0xfa8,
	0xf9e, 0xf92, 0xf88, 0xf7e, 0xf72, 0xf68, 0xf5c, 0xf52,
	0xf48, 0xf3e, 0xf32, 0xf28, 0xf1e, 0xf14, 0xf08, 0xefe,
	0xef4, 0xeea, 0xee0, 0xed4, 0xeca, 0xec0, 0xeb6, 0xeac,
	0xea2, 0xe98, 0xe8e, 0xe84, 0xe7a, 0xe70, 0xe66, 0xe5c,
	0xe52, 0xe48, 0xe3e, 0xe34, 0xe2a, 0xe20, 0xe16, 0xe0c,
	0xe04, 0xdfa, 0xdf0, 0xde6, 0xddc, 0xdd2, 0xdca, 0xdc0,
	0xdb6, 0xdac, 0xda4, 0xd9a, 0xd90, 0xd88, 0xd7e, 0xd74,
	0xd6a, 0xd62, 0xd58, 0xd50, 0xd46, 0xd3c, 0xd34, 0xd2a,
	0xd22, 0xd18, 0xd10, 0xd06, 0xcfe, 0xcf4, 0xcec, 0xce2,
	0xcda, 0xcd0, 0xcc8, 0xcbe, 0xcb6, 0xcae, 0xca4, 0xc9c,
	0xc92, 0xc8a, 0xc82, 0xc78, 0xc70, 0xc68, 0xc60, 0xc56,
	0xc4e, 0xc46, 0xc3c, 0xc34, 0xc2c, 0xc24, 0xc1c, 0xc12,
	0xc0a, 0xc02, 0xbfa, 0xbf2, 0xbea, 0xbe0, 0xbd8, 0xbd0,
	0xbc8, 0xbc0, 0xbb8, 0xbb0, 0xba8, 0xba0, 0xb98, 0xb90,
	0xb88, 0xb80, 0xb78, 0xb70, 0xb68, 0xb60, 0xb58, 0xb50,
	0xb48, 0xb40, 0xb38, 0xb32, 0xb2a, 0xb22, 0xb1a, 0xb12,
	0xb0a, 0xb02, 0xafc, 0xaf4, 0xaec, 0xae4, 0xade, 0xad6,
	0xace, 0xac6, 0xac0, 0xab8, 0xab0, 0xaa8, 0xaa2, 0xa9a,
	0xa92, 0xa8c, 0xa84, 0xa7c, 0xa76, 0xa6e, 0xa68, 0xa60,
	0xa58, 0xa52, 0xa4a, 0xa44, 0xa3c, 0xa36, 0xa2e, 0xa28,
	0xa20, 0xa18, 0xa12, 0xa0c, 0xa04, 0x9fe, 0x9f6, 0x9f0,
	0x9e8, 0x9e2, 0x9da, 0x9d4, 0x9ce, 0x9c6, 0x9c0, 0x9b8,
	0x9b2, 0x9ac, 0x9a4, 0x99e, 0x998, 0x990, 0x98a, 0x984,
	0x97c, 0x976, 0x970, 0x96a, 0x962, 0x95c, 0x956, 0x950,
	0x948, 0x942, 0x93c, 0x936, 0x930, 0x928, 0x922, 0x91c,
	0x916, 0x910, 0x90a, 0x904, 0x8fc, 0x8f6, 0x8f0, 0x8ea,
	0x8e4, 0x8de, 0x8d8, 0x8d2, 0x8cc, 0x8c6, 0x8c0, 0x8ba,
	0x8b4, 0x8ae, 0x8a8, 0x8a2, 0x89c, 0x896, 0x890, 0x88a,
	0x884, 0x87e, 0x878, 0x872, 0x86c, 0x866, 0x860, 0x85a,
	0x854, 0x850, 0x84a, 0x844, 0x83e, 0x838, 0x832, 0x82c,
	0x828, 0x822, 0x81c, 0x816, 0x810, 0x80c, 0x806, 0x800,
}

// freq mult table multiplied by 2.
var mt = [16]uint8{
	1, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 20, 24, 24, 30, 30,
}

// ksl table.
var kslrom = [16]uint8{
	0, 32, 40, 45, 48, 51, 53, 55, 56, 58, 59, 60, 61, 62, 63, 64,
}

var kslshift = [4]uint8{
	8, 1, 2, 0,
}

// envelope generator constants.
var egIncstep = [4][4]uint8{
	{0, 0, 0, 0},
	{1, 0, 0, 0},
	{1, 0, 1, 0},
	{1, 1, 1, 0},
}

// address decoding.
var adSlot = [0x20]int8{
	0, 1, 2, 3, 4, 5, -1, -1, 6, 7, 8, 9, 10, 11, -1, -1,
	12, 13, 14, 15, 16, 17, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
}

var chSlot = [18]uint8{
	0, 1, 2, 6, 7, 8, 12, 13, 14, 18, 19, 20, 24, 25, 26, 30, 31, 32,
}

// slot is the per-operator state (opl3_slot).
type slot struct {
	channel *channel
	chip    *Chip
	mod     *int16
	trem    *uint8
	pgReset uint32
	pgPhase uint32
	pgInc   uint32
	out     int16
	fbmod   int16
	prout   int16
	egRout  uint16
	egOut   uint16
	// egTlKsl caches (regTl << 2) + (egKsl >> kslshift[regKsl]); maintained
	// by envelopeUpdateKSL whenever any of those inputs change.
	egTlKsl    uint16
	pgPhaseOut uint16
	key        uint8
	egGen      uint8
	regVib     uint8
	regMult    uint8
	regWf      uint8
	slotNum    uint8
	egKsl      uint8
	egKs       uint8
	regType    uint8
	regKsr     uint8
	regKsl     uint8
	regTl      uint8
	regAr      uint8
	regDr      uint8
	regSl      uint8
	regRr      uint8
	egRates    [4]uint8
	egRateHi   [4]uint8
	egRateLo   [4]uint8
}

// channel is the per-channel state (opl3_channel).
type channel struct {
	slotz  [2]*slot
	pair   *channel
	chip   *Chip
	out    [4]*int16
	outCnt uint8
	chtype uint8
	fNum   uint16
	block  uint8
	fb     uint8
	con    uint8
	alg    uint8
	ksv    uint8
	cha    uint16
	chb    uint16
	chc    uint16
	chd    uint16
	chNum  uint8
}

type writebuf struct {
	time uint64
	reg  uint16
	data uint8
}

// Chip is the full OPL3 emulator state (opl3_chip / opl3_chip).
type Chip struct {
	channel [18]channel
	slot    [36]slot
	timer   uint16
	egTimer uint64

	egTimerrem   uint8
	egState      uint8
	egAdd        uint8
	egTimerLo    uint8
	newm         uint8
	nts          uint8
	rhy          uint8
	vibpos       uint8
	vibshift     uint8
	tremolo      uint8
	tremolopos   uint8
	tremoloshift uint8
	tremoloDirty uint8

	noise   uint32
	zeromod int16
	// zeroTrem is always 0 and stands in for the C cast
	// (uint8_t*)&chip->zeromod: since zeromod is never written to a
	// non-zero value, its low byte read through a uint8_t* is always 0.
	zeroTrem uint8
	mixbuff  [4]int32

	rmHhBit2 uint8
	rmHhBit3 uint8
	rmHhBit7 uint8
	rmHhBit8 uint8
	rmTcBit3 uint8
	rmTcBit5 uint8

	// OPL3L resampler.
	rateratio  int32
	samplecnt  int32
	oldsamples [4]int16
	samples    [4]int16

	writebufSamplecnt uint64
	writebufCur       uint32
	writebufLast      uint32
	writebufLasttime  uint64
	writebuf          [writebufSize]writebuf
}

//
// Envelope generator
//

func opl3EnvelopeUpdateKSL(s *slot) {
	ksl := int32(kslrom[s.channel.fNum>>6])<<2 - int32(0x08-s.channel.block)<<5
	if ksl < 0 {
		ksl = 0
	}
	s.egKsl = uint8(ksl)
	s.egTlKsl = uint16(s.regTl)<<2 + (uint16(s.egKsl) >> kslshift[s.regKsl])
}

func opl3EnvelopeUpdateRate(s *slot) {
	s.egKs = s.channel.ksv >> ((s.regKsr ^ 1) << 1)
	for ii := 0; ii < 4; ii++ {
		rate := s.egKs + (s.egRates[ii] << 2)
		rateHi := rate >> 2
		if rateHi&0x10 != 0 {
			rateHi = 0x0f
		}
		s.egRateHi[ii] = rateHi
		s.egRateLo[ii] = rate & 0x03
	}
}

func opl3EnvelopeCalc(s *slot) {
	var regRate uint8
	reset := uint8(0)

	s.egOut = s.egRout + s.egTlKsl + uint16(*s.trem)
	if s.key != 0 && s.egGen == envRelease {
		reset = 1
		regRate = s.egRates[0]
	} else {
		regRate = s.egRates[s.egGen]
	}
	s.pgReset = uint32(reset)
	nonzero := regRate != 0

	var rateHi, rateLo uint8
	if reset != 0 {
		rateHi = s.egRateHi[0]
		rateLo = s.egRateLo[0]
	} else {
		rateHi = s.egRateHi[s.egGen]
		rateLo = s.egRateLo[s.egGen]
	}
	egShift := rateHi + s.chip.egAdd
	shift := uint8(0)
	if nonzero {
		if rateHi < 12 {
			if s.chip.egState != 0 {
				switch egShift {
				case 12:
					shift = 1
				case 13:
					shift = (rateLo >> 1) & 0x01
				case 14:
					shift = rateLo & 0x01
				}
			}
		} else {
			shift = (rateHi & 0x03) + egIncstep[rateLo][s.chip.egTimerLo]
			if shift&0x04 != 0 {
				shift = 0x03
			}
			if shift == 0 {
				shift = s.chip.egState
			}
		}
	}
	egRout := s.egRout
	egInc := int16(0)
	egOff := uint8(0)
	// Instant attack.
	if reset != 0 && rateHi == 0x0f {
		egRout = 0x00
	}
	// Envelope off.
	if (s.egRout & 0x1f8) == 0x1f8 {
		egOff = 1
	}
	if s.egGen != envAttack && reset == 0 && egOff != 0 {
		egRout = 0x1ff
	}
	switch s.egGen {
	case envAttack:
		if s.egRout == 0 {
			s.egGen = envDecay
		} else if s.key != 0 && shift > 0 && rateHi != 0x0f {
			egInc = int16(^int32(s.egRout) >> (4 - shift))
		}
	case envDecay:
		if (s.egRout >> 4) == uint16(s.regSl) {
			s.egGen = envSustain
		} else if egOff == 0 && reset == 0 && shift > 0 {
			egInc = int16(1) << (shift - 1)
		}
	case envSustain, envRelease:
		if egOff == 0 && reset == 0 && shift > 0 {
			egInc = int16(1) << (shift - 1)
		}
	}
	s.egRout = uint16((int32(egRout) + int32(egInc)) & 0x1ff)
	// Key off.
	if reset != 0 {
		s.egGen = envAttack
	}
	if s.key == 0 {
		s.egGen = envRelease
	}
}

func opl3EnvelopeKeyOn(s *slot, typ uint8) {
	s.key |= typ
}

func opl3EnvelopeKeyOff(s *slot, typ uint8) {
	s.key &^= typ
}

//
// Phase Generator
//

func opl3PhaseUpdateInc(s *slot) {
	basefreq := (uint32(s.channel.fNum) << s.channel.block) >> 1
	s.pgInc = (basefreq * uint32(mt[s.regMult])) >> 1
}

func opl3PhaseGenerate(s *slot) {
	chip := s.chip
	var phaseinc uint32
	if s.regVib != 0 {
		fNum := s.channel.fNum
		rng := int8((fNum >> 7) & 7)
		vibpos := chip.vibpos

		if vibpos&3 == 0 {
			rng = 0
		} else if vibpos&1 != 0 {
			rng >>= 1
		}
		rng >>= chip.vibshift

		if vibpos&4 != 0 {
			rng = -rng
		}
		fNum += uint16(rng)
		basefreq := (uint32(fNum) << s.channel.block) >> 1
		phaseinc = (basefreq * uint32(mt[s.regMult])) >> 1
	} else {
		phaseinc = s.pgInc
	}
	phase := uint16(s.pgPhase >> 9)
	if s.pgReset != 0 {
		s.pgPhase = 0
	}
	s.pgPhase += phaseinc
	noise := chip.noise
	s.pgPhaseOut = phase
	switch s.slotNum {
	case 13: // hh
		chip.rmHhBit2 = uint8(phase>>2) & 1
		chip.rmHhBit3 = uint8(phase>>3) & 1
		chip.rmHhBit7 = uint8(phase>>7) & 1
		chip.rmHhBit8 = uint8(phase>>8) & 1
		if chip.rhy&0x20 != 0 {
			rmXor := (chip.rmHhBit2 ^ chip.rmHhBit7) |
				(chip.rmHhBit3 ^ chip.rmTcBit5) |
				(chip.rmTcBit3 ^ chip.rmTcBit5)
			s.pgPhaseOut = uint16(rmXor) << 9
			if rmXor^uint8(noise&1) != 0 {
				s.pgPhaseOut |= 0xd0
			} else {
				s.pgPhaseOut |= 0x34
			}
		}
	case 16: // sd
		if chip.rhy&0x20 != 0 {
			s.pgPhaseOut = (uint16(chip.rmHhBit8) << 9) |
				(uint16(chip.rmHhBit8^uint8(noise&1)) << 8)
		}
	case 17: // tc
		if chip.rhy&0x20 != 0 {
			chip.rmTcBit3 = uint8(phase>>3) & 1
			chip.rmTcBit5 = uint8(phase>>5) & 1
			rmXor := (chip.rmHhBit2 ^ chip.rmHhBit7) |
				(chip.rmHhBit3 ^ chip.rmTcBit5) |
				(chip.rmTcBit3 ^ chip.rmTcBit5)
			s.pgPhaseOut = (uint16(rmXor) << 9) | 0x80
		}
	}
	nBit := uint8((noise>>14)^noise) & 0x01
	chip.noise = (noise >> 1) | (uint32(nBit) << 22)
}

//
// Slot
//

func opl3SlotWrite20(s *slot, data uint8) {
	if (data>>7)&0x01 != 0 {
		s.trem = &s.chip.tremolo
	} else {
		s.trem = &s.chip.zeroTrem
	}
	s.regVib = (data >> 6) & 0x01
	s.regType = (data >> 5) & 0x01
	if s.regType != 0 {
		s.egRates[2] = 0
	} else {
		s.egRates[2] = s.regRr
	}
	s.regKsr = (data >> 4) & 0x01
	s.regMult = data & 0x0f
	opl3EnvelopeUpdateRate(s)
	opl3PhaseUpdateInc(s)
}

func opl3SlotWrite40(s *slot, data uint8) {
	s.regKsl = (data >> 6) & 0x03
	s.regTl = data & 0x3f
	opl3EnvelopeUpdateKSL(s)
}

func opl3SlotWrite60(s *slot, data uint8) {
	s.regAr = (data >> 4) & 0x0f
	s.regDr = data & 0x0f
	s.egRates[0] = s.regAr
	s.egRates[1] = s.regDr
	opl3EnvelopeUpdateRate(s)
}

func opl3SlotWrite80(s *slot, data uint8) {
	s.regSl = (data >> 4) & 0x0f
	if s.regSl == 0x0f {
		s.regSl = 0x1f
	}
	s.regRr = data & 0x0f
	if s.regType != 0 {
		s.egRates[2] = 0
	} else {
		s.egRates[2] = s.regRr
	}
	s.egRates[3] = s.regRr
	opl3EnvelopeUpdateRate(s)
}

func opl3SlotWriteE0(s *slot, data uint8) {
	s.regWf = data & 0x07
	if s.chip.newm == 0x00 {
		s.regWf &= 0x03
	}
}

func opl3SlotGenerate(s *slot) {
	phase := s.pgPhaseOut + uint16(*s.mod)
	envelope := s.egOut
	wfData := logsinWF[s.regWf][phase&0x3ff]
	neg := uint16(int16(wfData) >> 15)
	level := uint32(wfData&0x7fff) + (uint32(envelope) << 3)
	if level > 0x1fff {
		level = 0x1fff
	}
	s.out = int16((exprom[level&0xff] >> (level >> 8)) ^ neg)
}

func opl3SlotGenerateSilent(s *slot) {
	phase := s.pgPhaseOut + uint16(*s.mod)
	wfData := logsinWF[s.regWf][phase&0x3ff]
	s.out = int16(wfData) >> 15
}

func opl3SlotCalcFB(s *slot) {
	if s.channel.fb != 0x00 {
		s.fbmod = int16((int32(s.prout) + int32(s.out)) >> (0x09 - s.channel.fb))
	} else {
		s.fbmod = 0
	}
	s.prout = s.out
}

//
// Channel
//

func opl3ChannelUpdateRhythm(chip *Chip, data uint8) {
	chip.rhy = data & 0x3f
	if chip.rhy&0x20 != 0 {
		channel6 := &chip.channel[6]
		channel7 := &chip.channel[7]
		channel8 := &chip.channel[8]
		channel6.out[0] = &channel6.slotz[1].out
		channel6.out[1] = &channel6.slotz[1].out
		channel6.out[2] = &chip.zeromod
		channel6.out[3] = &chip.zeromod
		channel6.outCnt = 2
		channel7.out[0] = &channel7.slotz[0].out
		channel7.out[1] = &channel7.slotz[0].out
		channel7.out[2] = &channel7.slotz[1].out
		channel7.out[3] = &channel7.slotz[1].out
		channel7.outCnt = 4
		channel8.out[0] = &channel8.slotz[0].out
		channel8.out[1] = &channel8.slotz[0].out
		channel8.out[2] = &channel8.slotz[1].out
		channel8.out[3] = &channel8.slotz[1].out
		channel8.outCnt = 4
		for chnum := 6; chnum < 9; chnum++ {
			chip.channel[chnum].chtype = chDrum
		}
		opl3ChannelSetupAlg(channel6)
		opl3ChannelSetupAlg(channel7)
		opl3ChannelSetupAlg(channel8)
		// hh
		if chip.rhy&0x01 != 0 {
			opl3EnvelopeKeyOn(channel7.slotz[0], egkDrum)
		} else {
			opl3EnvelopeKeyOff(channel7.slotz[0], egkDrum)
		}
		// tc
		if chip.rhy&0x02 != 0 {
			opl3EnvelopeKeyOn(channel8.slotz[1], egkDrum)
		} else {
			opl3EnvelopeKeyOff(channel8.slotz[1], egkDrum)
		}
		// tom
		if chip.rhy&0x04 != 0 {
			opl3EnvelopeKeyOn(channel8.slotz[0], egkDrum)
		} else {
			opl3EnvelopeKeyOff(channel8.slotz[0], egkDrum)
		}
		// sd
		if chip.rhy&0x08 != 0 {
			opl3EnvelopeKeyOn(channel7.slotz[1], egkDrum)
		} else {
			opl3EnvelopeKeyOff(channel7.slotz[1], egkDrum)
		}
		// bd
		if chip.rhy&0x10 != 0 {
			opl3EnvelopeKeyOn(channel6.slotz[0], egkDrum)
			opl3EnvelopeKeyOn(channel6.slotz[1], egkDrum)
		} else {
			opl3EnvelopeKeyOff(channel6.slotz[0], egkDrum)
			opl3EnvelopeKeyOff(channel6.slotz[1], egkDrum)
		}
	} else {
		for chnum := 6; chnum < 9; chnum++ {
			chip.channel[chnum].chtype = ch2op
			opl3ChannelSetupAlg(&chip.channel[chnum])
			opl3EnvelopeKeyOff(chip.channel[chnum].slotz[0], egkDrum)
			opl3EnvelopeKeyOff(chip.channel[chnum].slotz[1], egkDrum)
		}
	}
}

func opl3ChannelWriteA0(channel *channel, data uint8) {
	if channel.chip.newm != 0 && channel.chtype == ch4op2 {
		return
	}
	channel.fNum = (channel.fNum & 0x300) | uint16(data)
	channel.ksv = (channel.block << 1) |
		uint8((channel.fNum>>(0x09-channel.chip.nts))&0x01)
	opl3EnvelopeUpdateKSL(channel.slotz[0])
	opl3EnvelopeUpdateKSL(channel.slotz[1])
	opl3EnvelopeUpdateRate(channel.slotz[0])
	opl3EnvelopeUpdateRate(channel.slotz[1])
	opl3PhaseUpdateInc(channel.slotz[0])
	opl3PhaseUpdateInc(channel.slotz[1])
	if channel.chip.newm != 0 && channel.chtype == ch4op {
		channel.pair.fNum = channel.fNum
		channel.pair.ksv = channel.ksv
		opl3EnvelopeUpdateKSL(channel.pair.slotz[0])
		opl3EnvelopeUpdateKSL(channel.pair.slotz[1])
		opl3EnvelopeUpdateRate(channel.pair.slotz[0])
		opl3EnvelopeUpdateRate(channel.pair.slotz[1])
		opl3PhaseUpdateInc(channel.pair.slotz[0])
		opl3PhaseUpdateInc(channel.pair.slotz[1])
	}
}

func opl3ChannelWriteB0(channel *channel, data uint8) {
	if channel.chip.newm != 0 && channel.chtype == ch4op2 {
		return
	}
	channel.fNum = (channel.fNum & 0xff) | (uint16(data&0x03) << 8)
	channel.block = (data >> 2) & 0x07
	channel.ksv = (channel.block << 1) |
		uint8((channel.fNum>>(0x09-channel.chip.nts))&0x01)
	opl3EnvelopeUpdateKSL(channel.slotz[0])
	opl3EnvelopeUpdateKSL(channel.slotz[1])
	opl3EnvelopeUpdateRate(channel.slotz[0])
	opl3EnvelopeUpdateRate(channel.slotz[1])
	opl3PhaseUpdateInc(channel.slotz[0])
	opl3PhaseUpdateInc(channel.slotz[1])
	if channel.chip.newm != 0 && channel.chtype == ch4op {
		channel.pair.fNum = channel.fNum
		channel.pair.block = channel.block
		channel.pair.ksv = channel.ksv
		opl3EnvelopeUpdateKSL(channel.pair.slotz[0])
		opl3EnvelopeUpdateKSL(channel.pair.slotz[1])
		opl3EnvelopeUpdateRate(channel.pair.slotz[0])
		opl3EnvelopeUpdateRate(channel.pair.slotz[1])
		opl3PhaseUpdateInc(channel.pair.slotz[0])
		opl3PhaseUpdateInc(channel.pair.slotz[1])
	}
}

func opl3ChannelSetupAlg(channel *channel) {
	if channel.chtype == chDrum {
		if channel.chNum == 7 || channel.chNum == 8 {
			channel.slotz[0].mod = &channel.chip.zeromod
			channel.slotz[1].mod = &channel.chip.zeromod
			return
		}
		switch channel.alg & 0x01 {
		case 0x00:
			channel.slotz[0].mod = &channel.slotz[0].fbmod
			channel.slotz[1].mod = &channel.slotz[0].out
		case 0x01:
			channel.slotz[0].mod = &channel.slotz[0].fbmod
			channel.slotz[1].mod = &channel.chip.zeromod
		}
		return
	}
	if channel.alg&0x08 != 0 {
		return
	}
	if channel.alg&0x04 != 0 {
		channel.pair.out[0] = &channel.chip.zeromod
		channel.pair.out[1] = &channel.chip.zeromod
		channel.pair.out[2] = &channel.chip.zeromod
		channel.pair.out[3] = &channel.chip.zeromod
		channel.pair.outCnt = 0
		switch channel.alg & 0x03 {
		case 0x00:
			channel.pair.slotz[0].mod = &channel.pair.slotz[0].fbmod
			channel.pair.slotz[1].mod = &channel.pair.slotz[0].out
			channel.slotz[0].mod = &channel.pair.slotz[1].out
			channel.slotz[1].mod = &channel.slotz[0].out
			channel.out[0] = &channel.slotz[1].out
			channel.out[1] = &channel.chip.zeromod
			channel.out[2] = &channel.chip.zeromod
			channel.out[3] = &channel.chip.zeromod
			channel.outCnt = 1
		case 0x01:
			channel.pair.slotz[0].mod = &channel.pair.slotz[0].fbmod
			channel.pair.slotz[1].mod = &channel.pair.slotz[0].out
			channel.slotz[0].mod = &channel.chip.zeromod
			channel.slotz[1].mod = &channel.slotz[0].out
			channel.out[0] = &channel.pair.slotz[1].out
			channel.out[1] = &channel.slotz[1].out
			channel.out[2] = &channel.chip.zeromod
			channel.out[3] = &channel.chip.zeromod
			channel.outCnt = 2
		case 0x02:
			channel.pair.slotz[0].mod = &channel.pair.slotz[0].fbmod
			channel.pair.slotz[1].mod = &channel.chip.zeromod
			channel.slotz[0].mod = &channel.pair.slotz[1].out
			channel.slotz[1].mod = &channel.slotz[0].out
			channel.out[0] = &channel.pair.slotz[0].out
			channel.out[1] = &channel.slotz[1].out
			channel.out[2] = &channel.chip.zeromod
			channel.out[3] = &channel.chip.zeromod
			channel.outCnt = 2
		case 0x03:
			channel.pair.slotz[0].mod = &channel.pair.slotz[0].fbmod
			channel.pair.slotz[1].mod = &channel.chip.zeromod
			channel.slotz[0].mod = &channel.pair.slotz[1].out
			channel.slotz[1].mod = &channel.chip.zeromod
			channel.out[0] = &channel.pair.slotz[0].out
			channel.out[1] = &channel.slotz[0].out
			channel.out[2] = &channel.slotz[1].out
			channel.out[3] = &channel.chip.zeromod
			channel.outCnt = 3
		}
	} else {
		switch channel.alg & 0x01 {
		case 0x00:
			channel.slotz[0].mod = &channel.slotz[0].fbmod
			channel.slotz[1].mod = &channel.slotz[0].out
			channel.out[0] = &channel.slotz[1].out
			channel.out[1] = &channel.chip.zeromod
			channel.out[2] = &channel.chip.zeromod
			channel.out[3] = &channel.chip.zeromod
			channel.outCnt = 1
		case 0x01:
			channel.slotz[0].mod = &channel.slotz[0].fbmod
			channel.slotz[1].mod = &channel.chip.zeromod
			channel.out[0] = &channel.slotz[0].out
			channel.out[1] = &channel.slotz[1].out
			channel.out[2] = &channel.chip.zeromod
			channel.out[3] = &channel.chip.zeromod
			channel.outCnt = 2
		}
	}
}

func opl3ChannelUpdateAlg(channel *channel) {
	channel.alg = channel.con
	if channel.chip.newm != 0 {
		if channel.chtype == ch4op {
			channel.pair.alg = 0x04 | (channel.con << 1) | channel.pair.con
			channel.alg = 0x08
			opl3ChannelSetupAlg(channel.pair)
		} else if channel.chtype == ch4op2 {
			channel.alg = 0x04 | (channel.pair.con << 1) | channel.con
			channel.pair.alg = 0x08
			opl3ChannelSetupAlg(channel)
		} else {
			opl3ChannelSetupAlg(channel)
		}
	} else {
		opl3ChannelSetupAlg(channel)
	}
}

func opl3ChannelWriteC0(channel *channel, data uint8) {
	channel.fb = (data & 0x0e) >> 1
	channel.con = data & 0x01
	opl3ChannelUpdateAlg(channel)
	if channel.chip.newm != 0 {
		if (data>>4)&0x01 != 0 {
			channel.cha = 0xffff
		} else {
			channel.cha = 0
		}
		if (data>>5)&0x01 != 0 {
			channel.chb = 0xffff
		} else {
			channel.chb = 0
		}
		if (data>>6)&0x01 != 0 {
			channel.chc = 0xffff
		} else {
			channel.chc = 0
		}
		if (data>>7)&0x01 != 0 {
			channel.chd = 0xffff
		} else {
			channel.chd = 0
		}
	} else {
		channel.cha = 0xffff
		channel.chb = 0xffff
		channel.chc = 0
		channel.chd = 0
	}
}

func opl3ChannelKeyOn(channel *channel) {
	if channel.chip.newm != 0 {
		if channel.chtype == ch4op {
			opl3EnvelopeKeyOn(channel.slotz[0], egkNorm)
			opl3EnvelopeKeyOn(channel.slotz[1], egkNorm)
			opl3EnvelopeKeyOn(channel.pair.slotz[0], egkNorm)
			opl3EnvelopeKeyOn(channel.pair.slotz[1], egkNorm)
		} else if channel.chtype == ch2op || channel.chtype == chDrum {
			opl3EnvelopeKeyOn(channel.slotz[0], egkNorm)
			opl3EnvelopeKeyOn(channel.slotz[1], egkNorm)
		}
	} else {
		opl3EnvelopeKeyOn(channel.slotz[0], egkNorm)
		opl3EnvelopeKeyOn(channel.slotz[1], egkNorm)
	}
}

func opl3ChannelKeyOff(channel *channel) {
	if channel.chip.newm != 0 {
		if channel.chtype == ch4op {
			opl3EnvelopeKeyOff(channel.slotz[0], egkNorm)
			opl3EnvelopeKeyOff(channel.slotz[1], egkNorm)
			opl3EnvelopeKeyOff(channel.pair.slotz[0], egkNorm)
			opl3EnvelopeKeyOff(channel.pair.slotz[1], egkNorm)
		} else if channel.chtype == ch2op || channel.chtype == chDrum {
			opl3EnvelopeKeyOff(channel.slotz[0], egkNorm)
			opl3EnvelopeKeyOff(channel.slotz[1], egkNorm)
		}
	} else {
		opl3EnvelopeKeyOff(channel.slotz[0], egkNorm)
		opl3EnvelopeKeyOff(channel.slotz[1], egkNorm)
	}
}

func opl3ChannelSet4Op(chip *Chip, data uint8) {
	for bit := uint8(0); bit < 6; bit++ {
		chnum := bit
		if bit >= 3 {
			chnum += 9 - 3
		}
		if (data>>bit)&0x01 != 0 {
			chip.channel[chnum].chtype = ch4op
			chip.channel[chnum+3].chtype = ch4op2
			opl3ChannelUpdateAlg(&chip.channel[chnum])
		} else {
			chip.channel[chnum].chtype = ch2op
			chip.channel[chnum+3].chtype = ch2op
			opl3ChannelUpdateAlg(&chip.channel[chnum])
			opl3ChannelUpdateAlg(&chip.channel[chnum+3])
		}
	}
}

func opl3ClipSample(sample int32) int16 {
	if sample > 32767 {
		sample = 32767
	} else if sample < -32768 {
		sample = -32768
	}
	return int16(sample)
}

func opl3ProcessSlot(s *slot) {
	// Fast path for fully-attenuated key-off non-rhythm slots.
	if s.key == 0 && s.egRout == 0x1ff &&
		s.slotNum != 13 && s.slotNum != 16 && s.slotNum != 17 {
		chip := s.chip
		noise := chip.noise
		nBit := uint8((noise>>14)^noise) & 0x01

		if s.channel.fb == 0 && s.pgInc == 0 && s.out == 0 &&
			*s.mod == 0 && s.egTlKsl == 0 && *s.trem == 0 &&
			s.pgPhase == 0 && s.regVib == 0 && s.regWf == 0 {
			s.fbmod = 0
			s.prout = 0
			s.egOut = 0x1ff
			s.pgReset = 0
			s.egGen = envRelease
			s.pgPhaseOut = 0
			chip.noise = (noise >> 1) | (uint32(nBit) << 22)
			return
		}

		opl3SlotCalcFB(s)

		s.egOut = s.egRout + s.egTlKsl + uint16(*s.trem)
		s.pgReset = 0
		s.egGen = envRelease

		var phaseinc uint32
		if s.regVib != 0 {
			fNum := s.channel.fNum
			rng := int8((fNum >> 7) & 7)
			vibpos := chip.vibpos

			if vibpos&3 == 0 {
				rng = 0
			} else if vibpos&1 != 0 {
				rng >>= 1
			}
			rng >>= chip.vibshift

			if vibpos&4 != 0 {
				rng = -rng
			}
			fNum += uint16(rng)
			phaseinc = ((uint32(fNum) << s.channel.block) >> 1) *
				uint32(mt[s.regMult]) >> 1
		} else {
			phaseinc = s.pgInc
		}

		phase := uint16(s.pgPhase >> 9)
		s.pgPhase += phaseinc
		s.pgPhaseOut = phase
		chip.noise = (noise >> 1) | (uint32(nBit) << 22)

		opl3SlotGenerateSilent(s)
		return
	}
	if s.egGen == envSustain && s.key != 0 && s.egRates[envSustain] == 0 {
		opl3SlotCalcFB(s)
		s.egOut = s.egRout + s.egTlKsl + uint16(*s.trem)
		s.pgReset = 0
		if (s.egRout & 0x1f8) == 0x1f8 {
			s.egRout = 0x1ff
		}

		if s.regVib == 0 &&
			s.slotNum != 13 && s.slotNum != 16 && s.slotNum != 17 {
			chip := s.chip
			noise := chip.noise
			nBit := uint8((noise>>14)^noise) & 0x01
			phase := uint16(s.pgPhase >> 9)

			s.pgPhase += s.pgInc
			s.pgPhaseOut = phase
			chip.noise = (noise >> 1) | (uint32(nBit) << 22)
		} else {
			opl3PhaseGenerate(s)
		}

		opl3SlotGenerate(s)
		return
	}
	opl3SlotCalcFB(s)
	opl3EnvelopeCalc(s)
	opl3PhaseGenerate(s)
	opl3SlotGenerate(s)
}

func opl3Generate4Ch(chip *Chip, buf4 []int16) {
	var mix [2]int32
	var accm int16

	buf4[1] = opl3ClipSample(chip.mixbuff[1])
	buf4[3] = opl3ClipSample(chip.mixbuff[3])

	// OPL_QUIRK_CHANNELSAMPLEDELAY == 1
	for ii := 0; ii < 15; ii++ {
		opl3ProcessSlot(&chip.slot[ii])
	}

	mix[0], mix[1] = 0, 0
	for ii := 0; ii < 18; ii++ {
		ch := &chip.channel[ii]
		if ch.outCnt == 0 {
			continue
		}
		if ch.cha|ch.chc == 0 {
			continue
		}
		out := &ch.out
		accm = *out[0]
		if ch.outCnt > 1 {
			accm += *out[1]
			if ch.outCnt > 2 {
				accm += *out[2]
				if ch.outCnt > 3 {
					accm += *out[3]
				}
			}
		}
		mix[0] += int32(int16(uint16(accm) & ch.cha))
		mix[1] += int32(int16(uint16(accm) & ch.chc))
	}
	chip.mixbuff[0] = mix[0]
	chip.mixbuff[2] = mix[1]

	for ii := 15; ii < 18; ii++ {
		opl3ProcessSlot(&chip.slot[ii])
	}

	buf4[0] = opl3ClipSample(chip.mixbuff[0])
	buf4[2] = opl3ClipSample(chip.mixbuff[2])

	for ii := 18; ii < 33; ii++ {
		opl3ProcessSlot(&chip.slot[ii])
	}

	mix[0], mix[1] = 0, 0
	for ii := 0; ii < 18; ii++ {
		ch := &chip.channel[ii]
		if ch.outCnt == 0 {
			continue
		}
		out := &ch.out
		accm = *out[0]
		if ch.outCnt > 1 {
			accm += *out[1]
			if ch.outCnt > 2 {
				accm += *out[2]
				if ch.outCnt > 3 {
					accm += *out[3]
				}
			}
		}
		mix[0] += int32(int16(uint16(accm) & ch.chb))
		mix[1] += int32(int16(uint16(accm) & ch.chd))
	}
	chip.mixbuff[1] = mix[0]
	chip.mixbuff[3] = mix[1]

	for ii := 33; ii < 36; ii++ {
		opl3ProcessSlot(&chip.slot[ii])
	}

	updateTremolo := chip.tremoloDirty
	if (chip.timer & 0x3f) == 0x3f {
		chip.tremolopos++
		if chip.tremolopos == 210 {
			chip.tremolopos = 0
		}
		updateTremolo = 1
	}
	if updateTremolo != 0 {
		if chip.tremolopos < 105 {
			chip.tremolo = chip.tremolopos >> chip.tremoloshift
		} else {
			chip.tremolo = (210 - chip.tremolopos) >> chip.tremoloshift
		}
		chip.tremoloDirty = 0
	}

	if (chip.timer & 0x3ff) == 0x3ff {
		chip.vibpos = (chip.vibpos + 1) & 7
	}

	chip.timer++

	if chip.egState != 0 {
		egTimerLow := uint32(chip.egTimer) & 0x1fff
		if egTimerLow == 0 {
			chip.egAdd = 0
		} else {
			shift := uint8(bits.TrailingZeros32(egTimerLow))
			chip.egAdd = shift + 1
		}
		chip.egTimerLo = uint8(chip.egTimer & 0x3)
	}

	if chip.egTimerrem != 0 || chip.egState != 0 {
		if chip.egTimer == 0xfffffffff {
			chip.egTimer = 0
			chip.egTimerrem = 1
		} else {
			chip.egTimer++
			chip.egTimerrem = 0
		}
	}

	chip.egState ^= 1

	for {
		wb := &chip.writebuf[chip.writebufCur]
		if wb.time > chip.writebufSamplecnt {
			break
		}
		if wb.reg&0x200 == 0 {
			break
		}
		wb.reg &= 0x1ff
		chip.WriteReg(wb.reg, wb.data)
		chip.writebufCur = (chip.writebufCur + 1) % writebufSize
	}
	chip.writebufSamplecnt++
}

func opl3Generate(chip *Chip, buf []int16) {
	var samples [4]int16
	opl3Generate4Ch(chip, samples[:])
	buf[0] = samples[0]
	buf[1] = samples[1]
}

func opl3Generate4ChResampled(chip *Chip, buf4 []int16) {
	for chip.samplecnt >= chip.rateratio {
		chip.oldsamples[0] = chip.samples[0]
		chip.oldsamples[1] = chip.samples[1]
		chip.oldsamples[2] = chip.samples[2]
		chip.oldsamples[3] = chip.samples[3]
		opl3Generate4Ch(chip, chip.samples[:])
		chip.samplecnt -= chip.rateratio
	}
	buf4[0] = int16((int32(chip.oldsamples[0])*(chip.rateratio-chip.samplecnt) +
		int32(chip.samples[0])*chip.samplecnt) / chip.rateratio)
	buf4[1] = int16((int32(chip.oldsamples[1])*(chip.rateratio-chip.samplecnt) +
		int32(chip.samples[1])*chip.samplecnt) / chip.rateratio)
	buf4[2] = int16((int32(chip.oldsamples[2])*(chip.rateratio-chip.samplecnt) +
		int32(chip.samples[2])*chip.samplecnt) / chip.rateratio)
	buf4[3] = int16((int32(chip.oldsamples[3])*(chip.rateratio-chip.samplecnt) +
		int32(chip.samples[3])*chip.samplecnt) / chip.rateratio)
	chip.samplecnt += 1 << rsmFrac
}

func opl3GenerateResampled(chip *Chip, buf []int16) {
	var samples [4]int16
	opl3Generate4ChResampled(chip, samples[:])
	buf[0] = samples[0]
	buf[1] = samples[1]
}

func opl3Reset(chip *Chip, samplerate uint32) {
	*chip = Chip{}
	for slotnum := uint8(0); slotnum < 36; slotnum++ {
		s := &chip.slot[slotnum]
		s.chip = chip
		s.mod = &chip.zeromod
		s.egRout = 0x1ff
		s.egOut = 0x1ff
		s.egGen = envRelease
		s.trem = &chip.zeroTrem
		s.egRates[0], s.egRates[1], s.egRates[2], s.egRates[3] = 0, 0, 0, 0
		s.slotNum = slotnum
	}
	for channum := uint8(0); channum < 18; channum++ {
		ch := &chip.channel[channum]
		localChSlot := chSlot[channum]
		ch.slotz[0] = &chip.slot[localChSlot]
		ch.slotz[1] = &chip.slot[localChSlot+3]
		chip.slot[localChSlot].channel = ch
		chip.slot[localChSlot+3].channel = ch
		if (channum % 9) < 3 {
			ch.pair = &chip.channel[channum+3]
		} else if (channum % 9) < 6 {
			ch.pair = &chip.channel[channum-3]
		}
		ch.chip = chip
		ch.out[0] = &chip.zeromod
		ch.out[1] = &chip.zeromod
		ch.out[2] = &chip.zeromod
		ch.out[3] = &chip.zeromod
		ch.outCnt = 0
		ch.chtype = ch2op
		ch.cha = 0xffff
		ch.chb = 0xffff
		ch.chNum = channum
		opl3ChannelSetupAlg(ch)
	}
	chip.noise = 1
	chip.rateratio = int32((samplerate << rsmFrac) / 49716)
	chip.tremoloshift = 4
	chip.vibshift = 1
}

func opl3WriteReg(chip *Chip, reg uint16, v uint8) {
	high := uint8((reg >> 8) & 0x01)
	regm := reg & 0xff
	switch regm & 0xf0 {
	case 0x00:
		if high != 0 {
			switch regm & 0x0f {
			case 0x04:
				opl3ChannelSet4Op(chip, v)
			case 0x05:
				chip.newm = v & 0x01
			}
		} else {
			switch regm & 0x0f {
			case 0x08:
				chip.nts = (v >> 6) & 0x01
			}
		}
	case 0x20, 0x30:
		if adSlot[regm&0x1f] >= 0 {
			opl3SlotWrite20(&chip.slot[18*uint16(high)+uint16(adSlot[regm&0x1f])], v)
		}
	case 0x40, 0x50:
		if adSlot[regm&0x1f] >= 0 {
			opl3SlotWrite40(&chip.slot[18*uint16(high)+uint16(adSlot[regm&0x1f])], v)
		}
	case 0x60, 0x70:
		if adSlot[regm&0x1f] >= 0 {
			opl3SlotWrite60(&chip.slot[18*uint16(high)+uint16(adSlot[regm&0x1f])], v)
		}
	case 0x80, 0x90:
		if adSlot[regm&0x1f] >= 0 {
			opl3SlotWrite80(&chip.slot[18*uint16(high)+uint16(adSlot[regm&0x1f])], v)
		}
	case 0xe0, 0xf0:
		if adSlot[regm&0x1f] >= 0 {
			opl3SlotWriteE0(&chip.slot[18*uint16(high)+uint16(adSlot[regm&0x1f])], v)
		}
	case 0xa0:
		if (regm & 0x0f) < 9 {
			opl3ChannelWriteA0(&chip.channel[9*uint16(high)+(regm&0x0f)], v)
		}
	case 0xb0:
		if regm == 0xbd && high == 0 {
			tremoloshift := (((v >> 7) ^ 1) << 1) + 2
			if chip.tremoloshift != tremoloshift {
				chip.tremoloDirty = 1
			}
			chip.tremoloshift = tremoloshift
			chip.vibshift = ((v >> 6) & 0x01) ^ 1
			opl3ChannelUpdateRhythm(chip, v)
		} else if (regm & 0x0f) < 9 {
			opl3ChannelWriteB0(&chip.channel[9*uint16(high)+(regm&0x0f)], v)
			if v&0x20 != 0 {
				opl3ChannelKeyOn(&chip.channel[9*uint16(high)+(regm&0x0f)])
			} else {
				opl3ChannelKeyOff(&chip.channel[9*uint16(high)+(regm&0x0f)])
			}
		}
	case 0xc0:
		if (regm & 0x0f) < 9 {
			opl3ChannelWriteC0(&chip.channel[9*uint16(high)+(regm&0x0f)], v)
		}
	}
}

func opl3WriteRegBuffered(chip *Chip, reg uint16, v uint8) {
	writebufLast := chip.writebufLast
	wb := &chip.writebuf[writebufLast]

	if wb.reg&0x200 != 0 {
		chip.WriteReg(wb.reg&0x1ff, wb.data)

		chip.writebufCur = (writebufLast + 1) % writebufSize
		chip.writebufSamplecnt = wb.time
	}

	wb.reg = reg | 0x200
	wb.data = v
	time1 := chip.writebufLasttime + writebufDelay
	time2 := chip.writebufSamplecnt

	if time1 < time2 {
		time1 = time2
	}

	wb.time = time1
	chip.writebufLasttime = time1
	chip.writebufLast = (writebufLast + 1) % writebufSize
}

func opl3GenerateStream(chip *Chip, sndptr []int16, numsamples uint32) {
	off := 0
	for i := uint32(0); i < numsamples; i++ {
		opl3GenerateResampled(chip, sndptr[off:])
		off += 2
	}
}

//
// Public API
//

// NewChip allocates and resets an OPL3 chip configured for the given output
// sample rate (Hz). It is the equivalent of OPL3_Reset on a fresh chip.
func NewChip(sampleRate uint32) *Chip {
	c := &Chip{}
	opl3Reset(c, sampleRate)
	return c
}

// Reset re-initializes the chip for the given output sample rate (Hz).
// Equivalent to OPL3_Reset.
func (c *Chip) Reset(sampleRate uint32) {
	opl3Reset(c, sampleRate)
}

// WriteReg applies a register write immediately. Equivalent to OPL3_WriteReg.
func (c *Chip) WriteReg(reg uint16, val uint8) {
	opl3WriteReg(c, reg, val)
}

// WriteRegBuffered queues a register write to take effect after the
// OPL_WRITEBUF_DELAY sample delay honored by the chip's write buffer.
// Equivalent to OPL3_WriteRegBuffered.
func (c *Chip) WriteRegBuffered(reg uint16, val uint8) {
	opl3WriteRegBuffered(c, reg, val)
}

// Generate produces one stereo frame of unresampled output, writing two
// int16 samples (left, right) into buf (len(buf) must be >= 2).
// Equivalent to OPL3_Generate.
func (c *Chip) Generate(buf []int16) {
	opl3Generate(c, buf)
}

// GenerateResampled produces one stereo frame resampled to the chip's
// configured output rate, writing two int16 samples (left, right) into buf
// (len(buf) must be >= 2). Equivalent to OPL3_GenerateResampled.
func (c *Chip) GenerateResampled(buf []int16) {
	opl3GenerateResampled(c, buf)
}

// GenerateStream produces numFrames stereo frames (2*numFrames int16
// samples) of resampled output into buf. Equivalent to OPL3_GenerateStream.
func (c *Chip) GenerateStream(buf []int16, numFrames int) {
	opl3GenerateStream(c, buf, uint32(numFrames))
}
