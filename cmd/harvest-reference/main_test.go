// Copyright (c) 2026 cloud-boot contributors
// SPDX-License-Identifier: GPL-2.0-only

package main

import (
	"bytes"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	godoom "github.com/cloud-boot/godoom"
	"github.com/cloud-boot/godoom/backend/audiolog"
)

func TestParseKey_AllForms(t *testing.T) {
	cases := []struct {
		in   string
		want uint8
		err  bool
	}{
		{"ENTER", godoom.KEY_ENTER, false},
		{"esc", godoom.KEY_ESCAPE, false},
		{"escape", godoom.KEY_ESCAPE, false},
		{"KEY_UPARROW", godoom.KEY_UPARROW1, false},
		{"down", godoom.KEY_DOWNARROW1, false},
		{"left", godoom.KEY_LEFTARROW1, false},
		{"right", godoom.KEY_RIGHTARROW1, false},
		{"fire", godoom.KEY_FIRE1, false},
		{"use", godoom.KEY_USE1, false},
		{"space", godoom.KEY_USE1, false},
		{"tab", godoom.KEY_TAB, false},
		{"0x42", 0x42, false},
		{"0XFF", 0xff, false},
		{"123", 123, false},
		{"0xZZ", 0, true},
		{"unknownsymbol", 0, true},
		{"99999", 0, true}, // overflows uint8
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseKey(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("parseKey(%q): want error, got nil (val=%d)", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseKey(%q): unexpected err: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("parseKey(%q): got %d want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseCheckpoints(t *testing.T) {
	cases := []struct {
		in   string
		want []int32
		err  bool
	}{
		{"35,70,140", []int32{35, 70, 140}, false},
		{"35", []int32{35}, false},
		{" 35 , 70 ", []int32{35, 70}, false},
		{",,35,,", []int32{35}, false},
		{"", nil, true},
		{"notanum", nil, true},
		{"35,bad,70", nil, true},
		{",,,", nil, true}, // no valid checkpoints parsed
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseCheckpoints(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("want err, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}

func TestParseScript_HappyPath(t *testing.T) {
	in := strings.NewReader(`
# leading comment
35:keydown:ENTER
36:keyup:ENTER # trailing comment
70:keydown:DOWN
   100  :  end
`)
	evs, raw, err := parseScript(in)
	if err != nil {
		t.Fatalf("parseScript: %v", err)
	}
	if len(evs) != 4 {
		t.Fatalf("got %d events want 4: %+v", len(evs), evs)
	}
	if evs[0].kind != "keydown" || evs[0].key != godoom.KEY_ENTER || evs[0].tic != 35 {
		t.Fatalf("bad first event: %+v", evs[0])
	}
	if evs[3].kind != "end" {
		t.Fatalf("bad final event: %+v", evs[3])
	}
	if len(raw) == 0 {
		t.Fatalf("script raw bytes empty")
	}
}

func TestParseScript_Errors(t *testing.T) {
	bad := []string{
		"nokind",                 // no colon
		"35:keydown",             // missing key
		"35:keyup",               // missing key
		"badnum:keydown:ENTER",   // bad tic
		"35:flerp:ENTER",         // unknown kind
		"35:keydown:NOPE",        // bad key
	}
	for _, line := range bad {
		t.Run(line, func(t *testing.T) {
			_, _, err := parseScript(strings.NewReader(line + "\n"))
			if err == nil {
				t.Fatalf("expected error for %q", line)
			}
		})
	}
}

func TestParseScript_ReadFailure(t *testing.T) {
	_, _, err := parseScript(errReader{})
	if err == nil {
		t.Fatalf("expected read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("forced") }

func TestWritePPM_RoundTripsHeaderAndPayload(t *testing.T) {
	dir := t.TempDir()
	w, h := 3, 2
	rgb := []byte{
		1, 2, 3, 4, 5, 6, 7, 8, 9,
		10, 11, 12, 13, 14, 15, 16, 17, 18,
	}
	path := filepath.Join(dir, "tiny.ppm")
	if err := writePPM(path, w, h, rgb); err != nil {
		t.Fatalf("writePPM: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	wantHeader := []byte("P6\n3 2\n255\n")
	if !bytes.HasPrefix(body, wantHeader) {
		t.Fatalf("header: got %q want %q", body[:len(wantHeader)], wantHeader)
	}
	payload := body[len(wantHeader):]
	if !bytes.Equal(payload, rgb) {
		t.Fatalf("payload mismatch: got %v want %v", payload, rgb)
	}
}

func TestWritePPM_BadLen(t *testing.T) {
	dir := t.TempDir()
	err := writePPM(filepath.Join(dir, "x.ppm"), 2, 2, []byte{1, 2, 3})
	if err == nil {
		t.Fatalf("want error for short rgb")
	}
}

func TestWritePPM_OpenFailure(t *testing.T) {
	err := writePPM("/nonexistent-dir-xyz/x.ppm", 1, 1, []byte{1, 2, 3})
	if err == nil {
		t.Fatalf("want error for unwritable path")
	}
}

func TestHarvestFrontend_DrawSnapshotAndStop(t *testing.T) {
	dir := t.TempDir()
	fe := newFrontend(nil, []int32{0}, dir)
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	fe.DrawFrame(img)
	if len(fe.results) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(fe.results))
	}
	cp := fe.results[0]
	if cp.Width != 4 || cp.Height != 4 {
		t.Fatalf("bad dims: %+v", cp)
	}
	if cp.FrameHash == "" {
		t.Fatalf("missing frame hash")
	}
	if _, err := os.Stat(filepath.Join(dir, cp.FramePPM)); err != nil {
		t.Fatalf("PPM not written: %v", err)
	}
	if !fe.stopCalled {
		t.Fatalf("expected Stop() after last checkpoint")
	}
	// SetTitle is still a no-op; CacheSound/PlaySound now feed the
	// R-doom1h audio recorder.
	fe.SetTitle("freedoom")
	fe.CacheSound("dspistol", []byte{1})
	fe.PlaySound("dspistol", 0, 100, 64)
	if got := len(fe.audio.Entries()); got != 2 {
		t.Fatalf("audio recorder should have 2 entries, got %d", got)
	}
}

func TestHarvestFrontend_AudioNilSafety(t *testing.T) {
	fe := &harvestFrontend{}
	fe.CacheSound("d", []byte{1})
	fe.PlaySound("d", 0, 100, 64)
}

func TestWriteAudioLog_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	rec := audiolog.New(nil)
	rec.Cache("dspistol", []byte{1, 2, 3})
	rec.Play("dspistol", 1, 100, 64)
	path := filepath.Join(dir, "audio_log.json")
	if err := writeAudioLog(path, rec); err != nil {
		t.Fatalf("writeAudioLog: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("audio log not written: %v", err)
	}
}

func TestWriteAudioLog_OpenFailure(t *testing.T) {
	rec := audiolog.New(nil)
	if err := writeAudioLog("/nonexistent-dir-xyz/audio_log.json", rec); err == nil {
		t.Fatalf("expected open error on unwritable path")
	}
}

func TestCompareAudioLogs_EqualPasses(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	if err := os.WriteFile(a, []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := os.WriteFile(b, []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	var buf bytes.Buffer
	if err := compareAudioLogs(a, b, &buf); err != nil {
		t.Fatalf("equal logs should pass, got %v", err)
	}
	if !strings.Contains(buf.String(), "GATE C-1 PASS") {
		t.Fatalf("expected PASS line, got %q", buf.String())
	}
}

func TestCompareAudioLogs_DiffFails(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	if err := os.WriteFile(a, []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := os.WriteFile(b, []byte("[{}]\n"), 0o644); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if err := compareAudioLogs(a, b, new(bytes.Buffer)); err == nil {
		t.Fatalf("differing logs should fail")
	}
}

func TestCompareAudioLogs_MissingOracle(t *testing.T) {
	dir := t.TempDir()
	b := filepath.Join(dir, "b.json")
	if err := os.WriteFile(b, []byte("[]"), 0o644); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if err := compareAudioLogs("/no/such/oracle.json", b, new(bytes.Buffer)); err == nil {
		t.Fatalf("missing oracle must error")
	}
}

func TestCompareAudioLogs_MissingFresh(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	if err := os.WriteFile(a, []byte("[]"), 0o644); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := compareAudioLogs(a, "/no/such/fresh.json", new(bytes.Buffer)); err == nil {
		t.Fatalf("missing fresh must error")
	}
}

func TestHarvestFrontend_GetEventDrains(t *testing.T) {
	events := []scriptEvent{
		{tic: 0, kind: "keydown", key: godoom.KEY_ENTER},
		{tic: 0, kind: "keyup", key: godoom.KEY_ENTER},
		{tic: 100000, kind: "keydown", key: godoom.KEY_ESCAPE}, // future, must NOT fire
	}
	fe := newFrontend(events, []int32{99999}, t.TempDir())

	var ev godoom.DoomEvent
	if !fe.GetEvent(&ev) || ev.Type != godoom.Ev_keydown || ev.Key != godoom.KEY_ENTER {
		t.Fatalf("first event mismatch: ok=true type=%v key=%v", ev.Type, ev.Key)
	}
	if !fe.GetEvent(&ev) || ev.Type != godoom.Ev_keyup {
		t.Fatalf("second event mismatch: %+v", ev)
	}
	// Future event must be gated by current tic.
	if fe.GetEvent(&ev) {
		t.Fatalf("future event should not have fired")
	}
}

func TestHarvestFrontend_GetEventEnd(t *testing.T) {
	fe := newFrontend([]scriptEvent{{tic: 0, kind: "end"}}, []int32{99999}, t.TempDir())
	var ev godoom.DoomEvent
	if fe.GetEvent(&ev) {
		t.Fatalf("end event must not surface as a key event")
	}
	if !fe.stopCalled {
		t.Fatalf("end event must call Stop")
	}
}

func TestHarvestFrontend_GetEventUnknownKindIsDropped(t *testing.T) {
	fe := newFrontend([]scriptEvent{{tic: 0, kind: "weirdkind"}}, []int32{99999}, t.TempDir())
	var ev godoom.DoomEvent
	if fe.GetEvent(&ev) {
		t.Fatalf("unknown kind must return false")
	}
}

func TestRun_FlagErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{"-zz-no-such-flag"}, &buf); err == nil {
		t.Fatalf("bad flag should error")
	}
}

func TestRun_MissingWAD(t *testing.T) {
	var buf bytes.Buffer
	err := run([]string{
		"-wad", "/no/such/file.wad",
		"-checkpoints", "1",
		"-out", t.TempDir(),
	}, &buf)
	if err == nil {
		t.Fatalf("missing WAD must error")
	}
}

func TestRun_BadScript(t *testing.T) {
	dir := t.TempDir()
	wad := filepath.Join(dir, "fake.wad")
	if err := os.WriteFile(wad, []byte("PWAD"), 0o644); err != nil {
		t.Fatalf("seed wad: %v", err)
	}
	script := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(script, []byte("garbage line\n"), 0o644); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	err := run([]string{
		"-wad", wad,
		"-script", script,
		"-checkpoints", "1",
		"-out", dir,
	}, new(bytes.Buffer))
	if err == nil {
		t.Fatalf("bad script must error before gore.Run")
	}
}

func TestRun_BadCheckpoints(t *testing.T) {
	dir := t.TempDir()
	wad := filepath.Join(dir, "fake.wad")
	if err := os.WriteFile(wad, []byte("PWAD"), 0o644); err != nil {
		t.Fatalf("seed wad: %v", err)
	}
	err := run([]string{
		"-wad", wad,
		"-checkpoints", "",
		"-out", dir,
	}, new(bytes.Buffer))
	if err == nil {
		t.Fatalf("empty -checkpoints must error")
	}
}

func TestRun_MissingScriptFile(t *testing.T) {
	dir := t.TempDir()
	wad := filepath.Join(dir, "fake.wad")
	if err := os.WriteFile(wad, []byte("PWAD"), 0o644); err != nil {
		t.Fatalf("seed wad: %v", err)
	}
	err := run([]string{
		"-wad", wad,
		"-script", "/no/such/script.txt",
		"-checkpoints", "1",
		"-out", dir,
	}, new(bytes.Buffer))
	if err == nil {
		t.Fatalf("missing script must error")
	}
}

func TestRun_MkdirFailure(t *testing.T) {
	// Use an existing regular FILE as -out; MkdirAll then fails because
	// a non-directory exists at that path.
	dir := t.TempDir()
	wad := filepath.Join(dir, "fake.wad")
	if err := os.WriteFile(wad, []byte("PWAD"), 0o644); err != nil {
		t.Fatalf("seed wad: %v", err)
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	err := run([]string{
		"-wad", wad,
		"-checkpoints", "1",
		"-out", filepath.Join(blocker, "subdir"),
	}, new(bytes.Buffer))
	if err == nil {
		t.Fatalf("expected mkdir failure")
	}
}
