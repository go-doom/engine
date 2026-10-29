// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

package ociblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// mirror serves one repository the way wasmdesk/ociapps-static lays it out.
type mirror struct {
	repo     string
	manifest []byte
	blobs    map[string][]byte
	// lie, when set, replaces the blob body served for any digest.
	lie []byte
	// contentLength, when non-empty, overrides the Content-Length header.
	contentLength string
}

func (m *mirror) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/site/v2/"+m.repo+"/manifests/latest":
		w.Header().Set("Content-Type", MediaTypeOCIManifest)
		w.Write(m.manifest)
	case strings.HasPrefix(r.URL.Path, "/site/v2/"+m.repo+"/blobs/"):
		d := strings.TrimPrefix(r.URL.Path, "/site/v2/"+m.repo+"/blobs/")
		b, ok := m.blobs[d]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if m.lie != nil {
			b = m.lie
		}
		if m.contentLength != "" {
			w.Header().Set("Content-Length", m.contentLength)
		}
		w.Write(b)
	default:
		http.NotFound(w, r)
	}
}

func ociappsManifest(file string, data []byte) []byte {
	d := digestOf(data)
	b, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     MediaTypeOCIManifest,
		"config":        map[string]any{"mediaType": "application/json", "digest": digestOf([]byte("{}")), "size": 2},
		"layers":        []map[string]any{{"mediaType": "application/octet-stream", "digest": d, "size": len(data)}},
		"annotations":   map[string]string{AnnotationOCIAppsPrefix + file: d},
	})
	return b
}

func titleManifest(file string, data []byte) []byte {
	b, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"layers": []map[string]any{{
			"mediaType":   "application/octet-stream",
			"digest":      digestOf(data),
			"size":        len(data),
			"annotations": map[string]string{AnnotationTitle: file},
		}},
	})
	return b
}

func serve(t *testing.T, m *mirror) *Client {
	t.Helper()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL + "/site/", HTTP: srv.Client()}
}

var wad = bytes.Repeat([]byte("IWAD-payload-"), 10000)

func TestFetchOCIAppsAnnotation(t *testing.T) {
	c := serve(t, &mirror{repo: "doom-assets", manifest: ociappsManifest("freedoom1.wad", wad), blobs: map[string][]byte{digestOf(wad): wad}})
	var calls int
	var last int64
	got, err := c.Fetch(context.Background(), Request{
		Repo: "doom-assets", Tag: "latest", File: "freedoom1.wad", Want: digestOf(wad), MaxBytes: 1 << 20,
		Progress: func(n, total int64) { calls++; last = n; _ = total },
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(got, wad) {
		t.Fatalf("content differs")
	}
	if calls == 0 || last != int64(len(wad)) {
		t.Fatalf("progress: %d calls, last %d", calls, last)
	}
}

func TestFetchTitleAnnotation(t *testing.T) {
	c := serve(t, &mirror{repo: "doom-assets", manifest: titleManifest("freedoom1.wad", wad), blobs: map[string][]byte{digestOf(wad): wad}})
	got, err := c.Fetch(context.Background(), Request{Repo: "doom-assets", Tag: "latest", File: "freedoom1.wad", MaxBytes: 1 << 20})
	if err != nil || !bytes.Equal(got, wad) {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetchRejects(t *testing.T) {
	other := []byte("not the wad")
	good := func() *mirror {
		return &mirror{repo: "doom-assets", manifest: ociappsManifest("freedoom1.wad", wad), blobs: map[string][]byte{digestOf(wad): wad}}
	}
	cases := []struct {
		name string
		m    *mirror
		req  Request
		want error
	}{
		{"pinned digest differs", good(), Request{Want: digestOf(other)}, ErrWrongDigest},
		{"malformed pin", good(), Request{Want: "sha256:XYZ"}, ErrBadDigest},
		{"too large", good(), Request{MaxBytes: 100}, ErrTooLarge},
		{"missing file", good(), Request{File: "doom2.wad"}, ErrNotFound},
		{"bad repo", good(), Request{Repo: "../etc"}, ErrBadName},
		{"bad tag", good(), Request{Tag: "a/b"}, ErrBadName},
		{"tampered body same length", func() *mirror {
			m := good()
			m.lie = bytes.Repeat([]byte("X"), len(wad))
			return m
		}(), Request{}, ErrMismatch},
		{"short body", func() *mirror { m := good(); m.lie = wad[:100]; return m }(), Request{}, ErrSize},
		{"long body", func() *mirror { m := good(); m.lie = append(append([]byte{}, wad...), 'Z'); return m }(), Request{}, ErrSize},
		{"content-length over descriptor", func() *mirror {
			m := good()
			m.lie = append(append([]byte{}, wad...), 'Z')
			m.contentLength = strconv.Itoa(len(wad) + 1)
			return m
		}(), Request{}, ErrSize},
		{"manifest not found", &mirror{repo: "other"}, Request{}, ErrHTTP},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := c.req
			if req.Repo == "" {
				req.Repo = "doom-assets"
			}
			if req.Tag == "" {
				req.Tag = "latest"
			}
			if req.File == "" {
				req.File = "freedoom1.wad"
			}
			if req.MaxBytes == 0 {
				req.MaxBytes = 1 << 20
			}
			_, err := serve(t, c.m).Fetch(context.Background(), req)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestLocateRejectsMalformedDigests(t *testing.T) {
	m, err := parseManifest([]byte(`{"schemaVersion":2,"layers":[{"digest":"sha256:../../x","size":1}],"annotations":{"ociapps.path/f":"sha256:../../x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locate(m, "f"); !errors.Is(err, ErrBadDigest) {
		t.Fatalf("got %v, want ErrBadDigest", err)
	}
	m2, _ := parseManifest([]byte(`{"schemaVersion":2,"layers":[{"digest":"md5:00","size":1,"annotations":{"org.opencontainers.image.title":"f"}}]}`))
	if _, err := locate(m2, "f"); !errors.Is(err, ErrBadDigest) {
		t.Fatalf("title path: got %v, want ErrBadDigest", err)
	}
	if _, err := parseManifest([]byte(`{"schemaVersion":1}`)); err == nil {
		t.Fatal("schemaVersion 1 accepted")
	}
}

// FuzzManifest: no manifest makes parse+locate panic, and any digest locate
// returns is well-formed (so it can be put in a URL path as is).
func FuzzManifest(f *testing.F) {
	f.Add(ociappsManifest("freedoom1.wad", wad))
	f.Add(titleManifest("freedoom1.wad", wad))
	f.Add([]byte(`{"schemaVersion":2,"annotations":{"ociapps.path/freedoom1.wad":"sha256:/../"}}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := parseManifest(b)
		if err != nil {
			return
		}
		d, err := locate(m, "freedoom1.wad")
		if err == nil && !ValidDigest(d.Digest) {
			t.Fatalf("locate returned malformed digest %q", d.Digest)
		}
	})
}
