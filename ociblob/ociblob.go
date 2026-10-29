// Copyright (c) 2026 the go-doom/engine authors.
// SPDX-License-Identifier: GPL-2.0-only

// Package ociblob pulls one named file out of an OCI image held by a
// registry or a static /v2 mirror, and verifies it before handing it back.
//
// It implements the two pull endpoints of the OCI distribution spec
// (github.com/opencontainers/distribution-spec, spec.md "Pulling manifests"
// end-3 and "Pulling blobs" end-2):
//
//	GET <base>/v2/<name>/manifests/<tag>
//	GET <base>/v2/<name>/blobs/<digest>
//
// The file is located in the manifest by either of the two conventions in
// use: the image-level annotation "ociapps.path/<file>" -> digest written by
// wasmdesk/ociapps (the packer behind the wasmdesk /v2 mirrors), or a layer
// annotated "org.opencontainers.image.title": "<file>" (the image-spec
// pre-defined annotation, used by ORAS).
//
// Everything the server sends is untrusted. Following the image-spec
// descriptor rules ("the size of the content SHOULD be verified", "the byte
// content SHOULD be verified against the digest"), the manifest is read
// through a small limit, the digest must be a well-formed sha256 (the
// encoded portion matching /[a-f0-9]{64}/), the layer size must be within the
// caller's limit, the blob is read through that limit, and its length and
// sha256 are recomputed. A caller that knows the digest it expects passes it
// as Want, so the mirror cannot substitute other content at all.
package ociblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// MaxManifestBytes bounds the manifest body.
const MaxManifestBytes = 1 << 20

// Media types accepted for the manifest.
const (
	MediaTypeOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
)

// Annotation keys used to find a file in a manifest.
const (
	AnnotationOCIAppsPrefix = "ociapps.path/"
	AnnotationTitle         = "org.opencontainers.image.title"
)

// Sentinel errors, for errors.Is.
var (
	ErrBadName     = errors.New("ociblob: invalid repository, tag or file name")
	ErrBadDigest   = errors.New("ociblob: malformed digest")
	ErrNotFound    = errors.New("ociblob: file not in manifest")
	ErrWrongDigest = errors.New("ociblob: digest is not the pinned one")
	ErrTooLarge    = errors.New("ociblob: blob larger than the limit")
	ErrSize        = errors.New("ociblob: blob size differs from the descriptor")
	ErrMismatch    = errors.New("ociblob: blob content does not match its digest")
	ErrHTTP        = errors.New("ociblob: unexpected HTTP status")
)

// Request names one file to pull.
type Request struct {
	// Repo is the repository name ("doom-assets"), Tag the tag ("latest").
	Repo, Tag string
	// File is the file name inside the image ("freedoom1.wad").
	File string
	// Want, when non-empty, is the only digest accepted ("sha256:<hex>").
	Want string
	// MaxBytes bounds the blob size; it must be positive.
	MaxBytes int64
	// Progress, when non-nil, is called as the blob arrives with the bytes
	// received so far and the descriptor size.
	Progress func(got, total int64)
}

// Client pulls from one base URL, e.g. "https://wasmdesk.github.io/wasmbox"
// (the mirror lives at <base>/v2/...).
type Client struct {
	Base string
	HTTP *http.Client
}

var (
	// Distribution-spec <name> grammar: path components of lowercase
	// alphanumerics separated by '.', '_', '__', '-' runs, joined by '/'.
	repoRe = regexp.MustCompile(`^[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*$`)
	// Distribution-spec <reference> tag grammar.
	tagRe = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
	// Image-spec sha256 digest grammar.
	digestRe = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// ValidDigest reports whether d is a well-formed sha256 digest.
func ValidDigest(d string) bool { return digestRe.MatchString(d) }

type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}

type manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Layers        []descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations"`
}

// Fetch pulls and verifies the file named by req.
func (c *Client) Fetch(ctx context.Context, req Request) ([]byte, error) {
	if !repoRe.MatchString(req.Repo) || !tagRe.MatchString(req.Tag) || req.File == "" || req.MaxBytes <= 0 {
		return nil, ErrBadName
	}
	if req.Want != "" && !ValidDigest(req.Want) {
		return nil, fmt.Errorf("%w: pinned %q", ErrBadDigest, req.Want)
	}
	base := strings.TrimRight(c.Base, "/")
	m, err := c.manifest(ctx, base, req.Repo, req.Tag)
	if err != nil {
		return nil, err
	}
	layer, err := locate(m, req.File)
	if err != nil {
		return nil, err
	}
	if req.Want != "" && layer.Digest != req.Want {
		return nil, fmt.Errorf("%w: manifest names %s, pinned %s", ErrWrongDigest, layer.Digest, req.Want)
	}
	if layer.Size < 0 || layer.Size > req.MaxBytes {
		return nil, fmt.Errorf("%w: %d bytes (limit %d)", ErrTooLarge, layer.Size, req.MaxBytes)
	}
	return c.blob(ctx, base, req, layer)
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) get(ctx context.Context, url, accept string) (*http.Response, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		hr.Header.Set("Accept", accept)
	}
	resp, err := c.client().Do(hr)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: GET %s: %s", ErrHTTP, url, resp.Status)
	}
	return resp, nil
}

func (c *Client) manifest(ctx context.Context, base, repo, tag string) (*manifest, error) {
	resp, err := c.get(ctx, base+"/v2/"+repo+"/manifests/"+tag, MediaTypeOCIManifest+", "+MediaTypeDockerManifest)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("ociblob: read manifest: %w", err)
	}
	if len(body) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest over %d bytes", ErrTooLarge, MaxManifestBytes)
	}
	return parseManifest(body)
}

// parseManifest decodes an image manifest (schemaVersion 2).
func parseManifest(body []byte) (*manifest, error) {
	m := &manifest{}
	if err := json.Unmarshal(body, m); err != nil {
		return nil, fmt.Errorf("ociblob: decode manifest: %w", err)
	}
	if m.SchemaVersion != 2 {
		return nil, fmt.Errorf("ociblob: manifest schemaVersion %d, want 2", m.SchemaVersion)
	}
	return m, nil
}

// locate finds the layer that carries file, by the ociapps image annotation
// first, then by a layer's org.opencontainers.image.title. The digest it
// returns is well-formed.
func locate(m *manifest, file string) (descriptor, error) {
	if d, ok := m.Annotations[AnnotationOCIAppsPrefix+file]; ok {
		if !ValidDigest(d) {
			return descriptor{}, fmt.Errorf("%w: %q", ErrBadDigest, d)
		}
		for _, l := range m.Layers {
			if l.Digest == d {
				return l, nil
			}
		}
		return descriptor{}, fmt.Errorf("%w: %s annotated as %s but no such layer", ErrNotFound, file, d)
	}
	for _, l := range m.Layers {
		if l.Annotations[AnnotationTitle] == file {
			if !ValidDigest(l.Digest) {
				return descriptor{}, fmt.Errorf("%w: %q", ErrBadDigest, l.Digest)
			}
			return l, nil
		}
	}
	return descriptor{}, fmt.Errorf("%w: %s", ErrNotFound, file)
}

func (c *Client) blob(ctx context.Context, base string, req Request, layer descriptor) ([]byte, error) {
	resp, err := c.get(ctx, base+"/v2/"+req.Repo+"/blobs/"+layer.Digest, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > layer.Size {
		return nil, fmt.Errorf("%w: Content-Length %d, descriptor %d", ErrSize, resp.ContentLength, layer.Size)
	}
	buf := make([]byte, 0, layer.Size)
	h := sha256.New()
	chunk := make([]byte, 256<<10)
	r := io.LimitReader(resp.Body, layer.Size+1)
	for {
		n, rerr := r.Read(chunk)
		if n > 0 {
			if int64(len(buf)+n) > layer.Size {
				return nil, fmt.Errorf("%w: more than %d bytes", ErrSize, layer.Size)
			}
			buf = append(buf, chunk[:n]...)
			h.Write(chunk[:n])
			if req.Progress != nil {
				req.Progress(int64(len(buf)), layer.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("ociblob: read blob: %w", rerr)
		}
	}
	if int64(len(buf)) != layer.Size {
		return nil, fmt.Errorf("%w: got %d bytes, descriptor %d", ErrSize, len(buf), layer.Size)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != layer.Digest {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrMismatch, got, layer.Digest)
	}
	return buf, nil
}
