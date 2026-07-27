// SPDX-License-Identifier: GPL-2.0-or-later
// Go port of chocolate-doom i_oplmusic.c (Simon Howard et al.).
// Ported for the go-doom/engine authors.

// Package oplplayer is a pure-Go port of chocolate-doom's DMX OPL music
// player (i_oplmusic.c). It ties MIDI events and the GENMIDI instrument bank
// to OPL register writes, driving the Nuked OPL3 emulator to produce PCM.
//
// The chocolate-doom timer/callback model (OPL_SetCallback + the opl_sdl mixing
// clock) is replaced by a synchronous sample-clock render loop in Read: MIDI
// events are processed at their sample-quantized times exactly as the reference
// timer would, so the audio produced is equivalent to chocolate-doom.
package oplplayer

import (
	"github.com/go-doom/engine/music/genmidi"
	"github.com/go-doom/engine/music/midi"
	"github.com/go-doom/engine/music/mus"
	"github.com/go-doom/engine/music/opl"
)

// OPL register constants (opl.h).
const (
	numOperators = 21 // OPL_NUM_OPERATORS
	numVoices    = 9  // OPL_NUM_VOICES

	regWaveformEnable = 0x01
	regTimer1         = 0x02
	regTimer2         = 0x03
	regTimerCtrl      = 0x04
	regFMMode         = 0x08
	regNew            = 0x105

	regsTremolo  = 0x20
	regsLevel    = 0x40
	regsAttack   = 0x60
	regsSustain  = 0x80
	regsWaveform = 0xE0

	regsFreq1    = 0xA0
	regsFreq2    = 0xB0
	regsFeedback = 0xC0
)

// midiChannelsPerTrack mirrors MIDI_CHANNELS_PER_TRACK.
const midiChannelsPerTrack = 16

// percussionLogLen mirrors PERCUSSION_LOG_LEN.
const percussionLogLen = 16

// driverVer selects which DMX library version's quirks to emulate
// (opl_driver_ver_t). The default is opl_doom_1_9.
type driverVer int

const (
	drvDoom1_1_666 driverVer = iota // Doom 1 v1.666
	drvDoom2_1_666                  // Doom 2 v1.666, Hexen, Heretic
	drvDoom1_9                      // Doom v1.9, Strife
)

// channelData is opl_channel_data_t: state of one MIDI channel.
type channelData struct {
	instrument int // index into bank.Instruments (main_instrs base = 0)
	volume     int
	volumeBase int
	pan        int
	bend       int
}

// trackData is opl_track_data_t: a playing track's iterator.
type trackData struct {
	iter *midi.TrackIterator
}

// voice is opl_voice_s: a hardware OPL voice.
type voice struct {
	index    int
	op1, op2 int
	array    int // 0 or 0x100 (second OPL3 register bank)

	currentInstr      int // index into bank.Instruments, -1 if none
	currentInstrVoice uint

	channel *channelData

	key  uint
	note uint
	freq uint

	noteVolume uint
	carVolume  uint
	modVolume  uint
	regPan     int
	priority   uint
}

// traceEntry records a single OPL register write for the differential trace
// oracle.
type traceEntry struct {
	us  uint64
	reg int
	val int
}

// Player renders OPL music from MIDI data.
type Player struct {
	chip       *opl.Chip
	sampleRate int
	bank       *genmidi.Bank

	oplDrvVer     driverVer
	opl3mode      bool
	numOplVoices  int
	stereoCorrect bool

	startMusicVolume   int
	currentMusicVolume int

	voiceStore  [numVoices * 2]voice
	freeList    [numVoices * 2]*voice
	allocedList [numVoices * 2]*voice
	freeNum     int
	allocedNum  int

	channels [midiChannelsPerTrack]channelData

	file          *midi.File
	tracks        []trackData
	numTracks     int
	runningTracks int
	songLooping   bool

	ticksPerBeat uint
	usPerBeat    uint

	lastPerc      [percussionLogLen]uint8
	lastPercCount uint

	// scheduler state (replaces the opl_sdl timer clock).
	queue       *callbackQueue
	currentUs   uint64
	pauseOffset uint64
	paused      bool
	playing     bool

	// trace, when non-nil, captures every register write (test hook).
	trace *[]traceEntry
}

// New builds a player: it loads the GENMIDI bank, creates an OPL chip at
// sampleRate and runs the OPL register initialisation sequence. opl3 selects
// OPL3 (18-voice) mode.
func New(genmidiLump []byte, sampleRate int, opl3 bool) (*Player, error) {
	return newPlayer(genmidiLump, sampleRate, opl3, false)
}

func newPlayer(genmidiLump []byte, sampleRate int, opl3, traceEnabled bool) (*Player, error) {
	bank, err := genmidi.Load(genmidiLump)
	if err != nil {
		return nil, err
	}

	p := &Player{
		chip:               opl.NewChip(uint32(sampleRate)),
		sampleRate:         sampleRate,
		bank:               bank,
		oplDrvVer:          drvDoom1_9,
		opl3mode:           opl3,
		currentMusicVolume: 127,
		queue:              newQueue(),
	}
	if opl3 {
		p.numOplVoices = numVoices * 2
	} else {
		p.numOplVoices = numVoices
	}
	if traceEnabled {
		p.trace = &[]traceEntry{}
	}

	p.initRegisters(opl3)
	p.initVoices()
	return p, nil
}

// isPercussion reports whether the given instrument index is a percussion
// instrument (current_instr >= percussion_instrs in the reference).
func isPercussion(instr int) bool { return instr >= genmidi.NumInstruments }

// instr returns a pointer to the instrument at the given bank index.
func (p *Player) instr(index int) *genmidi.Instrument { return &p.bank.Instruments[index] }

// writeRegister writes an OPL register (OPL_WriteRegister). The 0x100 bit
// selects the second OPL3 register bank. Writes are captured for the trace
// oracle when enabled.
func (p *Player) writeRegister(reg, val int) {
	if p.trace != nil {
		*p.trace = append(*p.trace, traceEntry{us: p.currentUs, reg: reg, val: val})
	}
	p.chip.WriteRegBuffered(uint16(reg), uint8(val))
}

// initRegisters ports OPL_InitRegisters from opl.c.
func (p *Player) initRegisters(opl3 bool) {
	for r := regsLevel; r <= regsLevel+numOperators; r++ {
		p.writeRegister(r, 0x3f)
	}
	for r := regsAttack; r <= regsWaveform+numOperators; r++ {
		p.writeRegister(r, 0x00)
	}
	for r := 1; r < regsLevel; r++ {
		p.writeRegister(r, 0x00)
	}

	p.writeRegister(regTimerCtrl, 0x60)
	p.writeRegister(regTimerCtrl, 0x80)
	p.writeRegister(regWaveformEnable, 0x20)

	if opl3 {
		p.writeRegister(regNew, 0x01)
		for r := regsLevel; r <= regsLevel+numOperators; r++ {
			p.writeRegister(r|0x100, 0x3f)
		}
		for r := regsAttack; r <= regsWaveform+numOperators; r++ {
			p.writeRegister(r|0x100, 0x00)
		}
		for r := 1; r < regsLevel; r++ {
			p.writeRegister(r|0x100, 0x00)
		}
	}

	p.writeRegister(regFMMode, 0x40)

	if opl3 {
		p.writeRegister(regNew, 0x01)
	}
}

// initVoices ports InitVoices.
func (p *Player) initVoices() {
	p.freeNum = p.numOplVoices
	p.allocedNum = 0

	for i := 0; i < p.numOplVoices; i++ {
		v := &p.voiceStore[i]
		v.index = i % numVoices
		v.op1 = voiceOperators[0][i%numVoices]
		v.op2 = voiceOperators[1][i%numVoices]
		v.array = (i / numVoices) << 8
		v.currentInstr = -1
		p.freeList[i] = v
	}
}

// getFreeVoice ports GetFreeVoice.
func (p *Player) getFreeVoice() *voice {
	if p.freeNum == 0 {
		return nil
	}

	result := p.freeList[0]
	p.freeNum--
	for i := 0; i < p.freeNum; i++ {
		p.freeList[i] = p.freeList[i+1]
	}

	p.allocedList[p.allocedNum] = result
	p.allocedNum++
	return result
}

// releaseVoice ports ReleaseVoice.
func (p *Player) releaseVoice(index int) {
	// Doom 2 1.666 OPL crash emulation.
	if index >= p.allocedNum {
		p.allocedNum = 0
		p.freeNum = 0
		return
	}

	v := p.allocedList[index]
	p.voiceKeyOff(v)

	v.channel = nil
	v.note = 0

	doubleVoice := v.currentInstrVoice != 0

	p.allocedNum--
	for i := index; i < p.allocedNum; i++ {
		p.allocedList[i] = p.allocedList[i+1]
	}

	p.freeList[p.freeNum] = v
	p.freeNum++

	if doubleVoice && p.oplDrvVer < drvDoom1_9 {
		p.releaseVoice(index)
	}
}

// loadOperatorData ports LoadOperatorData.
func (p *Player) loadOperatorData(operator int, data *genmidi.Operator, maxLevel bool, volume *uint) {
	level := int(data.Scale)
	if maxLevel {
		level |= 0x3f
	} else {
		level |= int(data.Level)
	}

	*volume = uint(level)

	p.writeRegister(regsLevel+operator, level)
	p.writeRegister(regsTremolo+operator, int(data.TremoloVibrato))
	p.writeRegister(regsAttack+operator, int(data.AttackDecay))
	p.writeRegister(regsSustain+operator, int(data.SustainRelease))
	p.writeRegister(regsWaveform+operator, int(data.Waveform))
}

// setVoiceInstrument ports SetVoiceInstrument.
func (p *Player) setVoiceInstrument(v *voice, instr int, instrVoice uint) {
	if v.currentInstr == instr && v.currentInstrVoice == instrVoice {
		return
	}

	v.currentInstr = instr
	v.currentInstrVoice = instrVoice

	data := &p.instr(instr).Voices[instrVoice]

	modulating := (data.Feedback & 0x01) == 0

	p.loadOperatorData(v.op2|v.array, &data.Carrier, true, &v.carVolume)
	p.loadOperatorData(v.op1|v.array, &data.Modulator, !modulating, &v.modVolume)

	p.writeRegister((regsFeedback+v.index)|v.array, int(data.Feedback)|v.regPan)

	v.priority = 0x0f - uint(data.Carrier.AttackDecay>>4) + 0x0f - uint(data.Carrier.SustainRelease&0x0f)
}

// setVoiceVolume ports SetVoiceVolume.
func (p *Player) setVoiceVolume(v *voice, volume uint) {
	v.noteVolume = volume

	oplVoice := &p.instr(v.currentInstr).Voices[v.currentInstrVoice]

	midiVolume := 2 * (volumeMappingTable[v.channel.volume] + 1)
	fullVolume := (volumeMappingTable[v.noteVolume] * midiVolume) >> 9

	carVolume := 0x3f - fullVolume

	if carVolume != (v.carVolume & 0x3f) {
		v.carVolume = carVolume | (v.carVolume & 0xc0)

		p.writeRegister((regsLevel+v.op2)|v.array, int(v.carVolume))

		if (oplVoice.Feedback&0x01) != 0 && oplVoice.Modulator.Level != 0x3f {
			modVolume := uint(oplVoice.Modulator.Level)
			if modVolume < carVolume {
				modVolume = carVolume
			}

			modVolume |= v.modVolume & 0xc0

			if modVolume != v.modVolume {
				v.modVolume = modVolume
				p.writeRegister((regsLevel+v.op1)|v.array,
					int(modVolume|uint(oplVoice.Modulator.Scale&0xc0)))
			}
		}
	}
}

// setVoicePan ports SetVoicePan.
func (p *Player) setVoicePan(v *voice, pan int) {
	v.regPan = pan
	oplVoice := &p.instr(v.currentInstr).Voices[v.currentInstrVoice]
	p.writeRegister((regsFeedback+v.index)|v.array, int(oplVoice.Feedback)|pan)
}

// setMusicVolume ports I_OPL_SetMusicVolume.
func (p *Player) setMusicVolume(volume int) {
	if p.currentMusicVolume == volume {
		return
	}

	p.currentMusicVolume = volume

	for i := 0; i < midiChannelsPerTrack; i++ {
		if i == 15 {
			p.setChannelVolume(&p.channels[i], volume, false)
		} else {
			p.setChannelVolume(&p.channels[i], p.channels[i].volumeBase, false)
		}
	}
}

// voiceKeyOff ports VoiceKeyOff.
func (p *Player) voiceKeyOff(v *voice) {
	p.writeRegister((regsFreq2+v.index)|v.array, int(v.freq>>8))
}

// trackChannelForEvent ports TrackChannelForEvent (the MUS 9<->15 swap).
func (p *Player) trackChannelForEvent(ev *midi.Event) *channelData {
	channelNum := int(ev.Channel)
	if channelNum == 9 {
		channelNum = 15
	} else if channelNum == 15 {
		channelNum = 9
	}
	return &p.channels[channelNum]
}

// keyOffEvent ports KeyOffEvent.
func (p *Player) keyOffEvent(ev *midi.Event) {
	channel := p.trackChannelForEvent(ev)
	key := uint(ev.Param1)

	for i := 0; i < p.allocedNum; i++ {
		if p.allocedList[i].channel == channel && p.allocedList[i].key == key {
			p.releaseVoice(i)
			i--
		}
	}
}

// replaceExistingVoice ports ReplaceExistingVoice (opl_doom_1_9).
func (p *Player) replaceExistingVoice() {
	result := 0
	for i := 0; i < p.allocedNum; i++ {
		if p.allocedList[i].currentInstrVoice != 0 ||
			!p.channelLess(p.allocedList[i].channel, p.allocedList[result].channel) {
			result = i
		}
	}
	p.releaseVoice(result)
}

// replaceExistingVoiceDoom1 ports ReplaceExistingVoiceDoom1.
func (p *Player) replaceExistingVoiceDoom1() {
	result := 0
	for i := 0; i < p.allocedNum; i++ {
		if p.channelLess(p.allocedList[result].channel, p.allocedList[i].channel) {
			result = i
		}
	}
	p.releaseVoice(result)
}

// replaceExistingVoiceDoom2 ports ReplaceExistingVoiceDoom2.
func (p *Player) replaceExistingVoiceDoom2(channel *channelData) {
	result := 0
	priority := uint(0x8000)
	for i := 0; i < p.allocedNum-3; i++ {
		if p.allocedList[i].priority < priority &&
			!p.channelLess(p.allocedList[i].channel, channel) {
			priority = p.allocedList[i].priority
			result = i
		}
	}
	p.releaseVoice(result)
}

// channelLess reports whether channel a sorts before channel b, matching the
// pointer comparisons the reference performs on &channels[i].
func (p *Player) channelLess(a, b *channelData) bool {
	return p.channelIndex(a) < p.channelIndex(b)
}

func (p *Player) channelIndex(c *channelData) int {
	for i := range p.channels {
		if &p.channels[i] == c {
			return i
		}
	}
	return -1
}

// frequencyForVoice ports FrequencyForVoice.
func (p *Player) frequencyForVoice(v *voice) uint {
	gmVoice := &p.instr(v.currentInstr).Voices[v.currentInstrVoice]

	note := int(v.note)

	if p.instr(v.currentInstr).Flags&genmidi.FlagFixed == 0 {
		note += int(gmVoice.BaseNoteOffset)
	}

	for note < 0 {
		note += 12
	}
	for note > 95 {
		note -= 12
	}

	freqIndex := 64 + 32*note + v.channel.bend

	if v.currentInstrVoice != 0 {
		freqIndex += int(p.instr(v.currentInstr).FineTuning)/2 - 64
	}

	if freqIndex < 0 {
		freqIndex = 0
	}

	if freqIndex < 284 {
		return uint(frequencyCurve[freqIndex])
	}

	subIndex := (freqIndex - 284) % (12 * 32)
	octave := (freqIndex - 284) / (12 * 32)

	if octave >= 7 {
		octave = 7
	}

	return uint(frequencyCurve[subIndex+284]) | (uint(octave) << 10)
}

// updateVoiceFrequency ports UpdateVoiceFrequency.
func (p *Player) updateVoiceFrequency(v *voice) {
	freq := p.frequencyForVoice(v)

	if v.freq != freq {
		p.writeRegister((regsFreq1+v.index)|v.array, int(freq&0xff))
		p.writeRegister((regsFreq2+v.index)|v.array, int((freq>>8)|0x20))
		v.freq = freq
	}
}

// voiceKeyOn ports VoiceKeyOn.
func (p *Player) voiceKeyOn(channel *channelData, instrument int, instrumentVoice, note, key, volume uint) {
	if !p.opl3mode && p.oplDrvVer == drvDoom1_1_666 {
		instrumentVoice = 0
	}

	v := p.getFreeVoice()
	if v == nil {
		return
	}

	v.channel = channel
	v.key = key

	if p.instr(instrument).Flags&genmidi.FlagFixed != 0 {
		v.note = uint(p.instr(instrument).FixedNote)
	} else {
		v.note = note
	}

	v.regPan = channel.pan

	p.setVoiceInstrument(v, instrument, instrumentVoice)
	p.setVoiceVolume(v, volume)

	v.freq = 0
	p.updateVoiceFrequency(v)
}

// keyOnEvent ports KeyOnEvent.
func (p *Player) keyOnEvent(ev *midi.Event) {
	note := uint(ev.Param1)
	key := uint(ev.Param1)
	volume := uint(ev.Param2)

	if ev.Param2 == 0 {
		p.keyOffEvent(ev)
		return
	}

	channel := p.trackChannelForEvent(ev)

	var instrument int
	if ev.Channel == 9 {
		if key < 35 || key > 81 {
			return
		}
		instrument = genmidi.NumInstruments + int(key-35)
		p.lastPerc[p.lastPercCount] = uint8(key)
		p.lastPercCount = (p.lastPercCount + 1) % percussionLogLen
		note = 60
	} else {
		instrument = channel.instrument
	}

	doubleVoice := p.instr(instrument).Flags&genmidi.Flag2Voice != 0

	switch p.oplDrvVer {
	case drvDoom1_1_666:
		voicenum := 1
		if doubleVoice {
			voicenum = 2
		}
		if !p.opl3mode {
			voicenum = 1
		}
		for p.allocedNum > p.numOplVoices-voicenum {
			p.replaceExistingVoiceDoom1()
		}
		if doubleVoice {
			p.voiceKeyOn(channel, instrument, 1, note, key, volume)
		}
		p.voiceKeyOn(channel, instrument, 0, note, key, volume)
	case drvDoom2_1_666:
		if p.allocedNum == p.numOplVoices {
			p.replaceExistingVoiceDoom2(channel)
		}
		if p.allocedNum == p.numOplVoices-1 && doubleVoice {
			p.replaceExistingVoiceDoom2(channel)
		}
		if doubleVoice {
			p.voiceKeyOn(channel, instrument, 1, note, key, volume)
		}
		p.voiceKeyOn(channel, instrument, 0, note, key, volume)
	default:
		if p.freeNum == 0 {
			p.replaceExistingVoice()
		}
		p.voiceKeyOn(channel, instrument, 0, note, key, volume)
		if doubleVoice {
			p.voiceKeyOn(channel, instrument, 1, note, key, volume)
		}
	}
}

// programChangeEvent ports ProgramChangeEvent.
func (p *Player) programChangeEvent(ev *midi.Event) {
	channel := p.trackChannelForEvent(ev)
	channel.instrument = int(ev.Param1)
}

// setChannelVolume ports SetChannelVolume.
func (p *Player) setChannelVolume(channel *channelData, volume int, clipStart bool) {
	channel.volumeBase = volume

	if volume > p.currentMusicVolume {
		volume = p.currentMusicVolume
	}
	if clipStart && volume > p.startMusicVolume {
		volume = p.startMusicVolume
	}

	channel.volume = volume

	for i := 0; i < p.numOplVoices; i++ {
		if p.voiceStore[i].channel == channel {
			p.setVoiceVolume(&p.voiceStore[i], p.voiceStore[i].noteVolume)
		}
	}
}

// setChannelPan ports SetChannelPan.
func (p *Player) setChannelPan(channel *channelData, pan int) {
	if p.stereoCorrect {
		pan = 144 - pan
	}

	if p.opl3mode {
		var regPan int
		if pan >= 96 {
			regPan = 0x10
		} else if pan <= 48 {
			regPan = 0x20
		} else {
			regPan = 0x30
		}
		if channel.pan != regPan {
			channel.pan = regPan
			for i := 0; i < p.numOplVoices; i++ {
				if p.voiceStore[i].channel == channel {
					p.setVoicePan(&p.voiceStore[i], regPan)
				}
			}
		}
	}
}

// allNotesOff ports AllNotesOff.
func (p *Player) allNotesOff(channel *channelData) {
	for i := 0; i < p.allocedNum; i++ {
		if p.allocedList[i].channel == channel {
			p.releaseVoice(i)
			i--
		}
	}
}

// controllerEvent ports ControllerEvent.
func (p *Player) controllerEvent(ev *midi.Event) {
	channel := p.trackChannelForEvent(ev)
	controller := ev.Param1
	param := int(ev.Param2)

	switch controller {
	case 0x07: // MIDI_CONTROLLER_VOLUME_MSB
		p.setChannelVolume(channel, param, true)
	case 0x0A: // MIDI_CONTROLLER_PAN
		p.setChannelPan(channel, param)
	case 0x7B: // MIDI_CONTROLLER_ALL_NOTES_OFF
		p.allNotesOff(channel)
	}
}

// pitchBendEvent ports PitchBendEvent.
func (p *Player) pitchBendEvent(ev *midi.Event) {
	channel := p.trackChannelForEvent(ev)
	channel.bend = int(ev.Param2) - 64

	var updated [numVoices * 2]*voice
	var notUpdated [numVoices * 2]*voice
	updatedNum := 0
	notUpdatedNum := 0

	for i := 0; i < p.allocedNum; i++ {
		if p.allocedList[i].channel == channel {
			p.updateVoiceFrequency(p.allocedList[i])
			updated[updatedNum] = p.allocedList[i]
			updatedNum++
		} else {
			notUpdated[notUpdatedNum] = p.allocedList[i]
			notUpdatedNum++
		}
	}

	for i := 0; i < notUpdatedNum; i++ {
		p.allocedList[i] = notUpdated[i]
	}
	for i := 0; i < updatedNum; i++ {
		p.allocedList[i+notUpdatedNum] = updated[i]
	}
}

// metaSetTempo ports MetaSetTempo.
func (p *Player) metaSetTempo(tempo uint) {
	p.queue.adjustCallbacks(p.currentUs, float32(p.usPerBeat)/float32(tempo))
	p.usPerBeat = tempo
}

// metaEvent ports MetaEvent.
func (p *Player) metaEvent(ev *midi.Event) {
	if ev.MetaType == midi.MetaSetTempo {
		if len(ev.Data) == 3 {
			p.metaSetTempo(uint(ev.Data[0])<<16 | uint(ev.Data[1])<<8 | uint(ev.Data[2]))
		}
	}
}

// processEvent ports ProcessEvent.
func (p *Player) processEvent(ev *midi.Event) {
	switch ev.Type {
	case midi.NoteOff:
		p.keyOffEvent(ev)
	case midi.NoteOn:
		p.keyOnEvent(ev)
	case midi.Controller:
		p.controllerEvent(ev)
	case midi.ProgramChange:
		p.programChangeEvent(ev)
	case midi.PitchBend:
		p.pitchBendEvent(ev)
	case midi.Meta:
		p.metaEvent(ev)
	}
}

// setCallback ports OPL_SetCallback for the synchronous scheduler.
func (p *Player) setCallback(us uint64, e queueEntry) {
	e.time = p.currentUs - p.pauseOffset + us
	p.queue.push(e)
}

// restartSong ports RestartSong.
func (p *Player) restartSong() {
	p.runningTracks = p.numTracks
	p.startMusicVolume = p.currentMusicVolume

	for i := 0; i < p.numTracks; i++ {
		p.tracks[i].iter.Restart()
		p.scheduleTrack(i)
	}
	for i := 0; i < midiChannelsPerTrack; i++ {
		p.initChannel(&p.channels[i])
	}
}

// trackTimerCallback ports TrackTimerCallback.
func (p *Player) trackTimerCallback(trackIdx int) {
	ev, ok := p.tracks[trackIdx].iter.Next()
	if !ok {
		return
	}

	p.processEvent(ev)

	if ev.Type == midi.Meta && ev.MetaType == midi.MetaEndOfTrack {
		p.runningTracks--
		if p.runningTracks <= 0 && p.songLooping {
			p.setCallback(5000, queueEntry{kind: cbRestartSong})
		}
		return
	}

	p.scheduleTrack(trackIdx)
}

// scheduleTrack ports ScheduleTrack.
func (p *Player) scheduleTrack(trackIdx int) {
	nticks := uint64(p.tracks[trackIdx].iter.DeltaTime())
	us := (nticks * uint64(p.usPerBeat)) / uint64(p.ticksPerBeat)
	p.setCallback(us, queueEntry{kind: cbTrackTimer, track: trackIdx})
}

// initChannel ports InitChannel.
func (p *Player) initChannel(channel *channelData) {
	channel.instrument = 0
	channel.volume = p.currentMusicVolume
	channel.volumeBase = 100
	if channel.volume > channel.volumeBase {
		channel.volume = channel.volumeBase
	}
	channel.pan = 0x30
	channel.bend = 0
}

// invoke dispatches a scheduled callback.
func (p *Player) invoke(e queueEntry) {
	switch e.kind {
	case cbTrackTimer:
		p.trackTimerCallback(e.track)
	case cbRestartSong:
		p.restartSong()
	}
}

// RegisterSong parses MIDI (or MUS, converted on the fly) data ready for
// playback. It mirrors I_OPL_RegisterSong.
func (p *Player) RegisterSong(midiData []byte) error {
	data := midiData
	if mus.IsMUS(data) {
		conv, err := mus.Convert(data)
		if err != nil {
			return err
		}
		data = conv
	}

	f, err := midi.Parse(data)
	if err != nil {
		return err
	}
	p.file = f
	return nil
}

// Play starts playback of the registered song (I_OPL_PlaySong).
func (p *Player) Play(looping bool) {
	if p.file == nil {
		return
	}

	p.numTracks = p.file.NumTracks()
	p.tracks = make([]trackData, p.numTracks)
	p.runningTracks = p.numTracks
	p.songLooping = looping

	p.ticksPerBeat = uint(p.file.TimeDivisionTicks())
	p.usPerBeat = 500 * 1000

	p.startMusicVolume = p.currentMusicVolume

	for i := 0; i < p.numTracks; i++ {
		p.tracks[i].iter = p.file.IterateTrack(i)
		p.scheduleTrack(i)
	}
	for i := 0; i < midiChannelsPerTrack; i++ {
		p.initChannel(&p.channels[i])
	}

	p.paused = false
	p.playing = true
}

// Stop stops playback and frees all voices (I_OPL_StopSong).
func (p *Player) Stop() {
	p.queue.clear()

	for i := 0; i < midiChannelsPerTrack; i++ {
		p.allNotesOff(&p.channels[i])
	}

	p.tracks = nil
	p.numTracks = 0
	p.playing = false
}

// Pause pauses playback (I_OPL_PauseSong).
func (p *Player) Pause() {
	p.paused = true

	for i := 0; i < p.numOplVoices; i++ {
		if p.voiceStore[i].channel != nil && !isPercussion(p.voiceStore[i].currentInstr) {
			p.voiceKeyOff(&p.voiceStore[i])
		}
	}
}

// Resume resumes playback (I_OPL_ResumeSong).
func (p *Player) Resume() { p.paused = false }

// SetVolume sets the music volume (0..127) (I_OPL_SetMusicVolume).
func (p *Player) SetVolume(v int) { p.setMusicVolume(v) }

// IsPlaying reports whether a song is loaded and playing (I_OPL_MusicIsPlaying).
func (p *Player) IsPlaying() bool { return p.numTracks > 0 }

// advanceTime advances the sample clock by nsamples, firing any callbacks that
// are now due (opl_sdl.c AdvanceTime).
func (p *Player) advanceTime(nsamples int) {
	us := uint64(nsamples) * 1_000_000 / uint64(p.sampleRate)
	p.currentUs += us

	if p.paused {
		p.pauseOffset += us
	}

	for !p.queue.isEmpty() && p.currentUs >= p.queue.peek()+p.pauseOffset {
		e := p.queue.pop()
		p.invoke(e)
	}
}

// Read renders interleaved stereo int16 PCM into buf (len must be even),
// advancing the internal sample clock and processing MIDI events at their
// sample-quantized times exactly as the chocolate-doom timer would. It returns
// the number of stereo frames written (len(buf)/2). It mirrors the opl_sdl
// mixing callback (OPL_Mix_Callback).
func (p *Player) Read(buf []int16) int {
	frames := len(buf) / 2
	produced := 0

	for produced < frames {
		var nsamples int
		if p.paused || p.queue.isEmpty() {
			nsamples = frames - produced
		} else {
			nextCallbackTime := p.queue.peek() + p.pauseOffset
			var d uint64
			if nextCallbackTime > p.currentUs {
				d = nextCallbackTime - p.currentUs
			}
			ns := (d*uint64(p.sampleRate) + 1_000_000 - 1) / 1_000_000
			nsamples = int(ns)
			if nsamples > frames-produced {
				nsamples = frames - produced
			}
		}

		if nsamples > 0 {
			p.chip.GenerateStream(buf[produced*2:(produced+nsamples)*2], nsamples)
			produced += nsamples
		}

		p.advanceTime(nsamples)
	}

	return produced
}
