// Package mermaid is mermaid.js, vendored: the diagrams a page draws from its ```mermaid blocks,
// drawn as the Mermaid project draws them. It is mermaid 12.1.0's dist/mermaid.min.js, from the npm
// registry's tarball (sha512-wlVCp+8eTupfCeeFvoZNNiTuHrvag0P2jz/ILgb/f/6jkVokUefOcujefi8qUe/j2asHiSePncVsz/xzzA80LQ==),
// MIT-licensed (LICENSE), kept gzipped and unpacked once, when first asked for.
//
// To update it: take dist/mermaid.min.js from the new version's tarball, check the tarball against
// the registry's integrity, gzip it with -9 -n over mermaid.min.js.gz, copy its LICENSE, and change
// Version, the integrity above and the digest the test pins.
package mermaid

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"io"
	"sync"
)

// Version is the vendored mermaid's.
const Version = "12.1.0"

//go:embed mermaid.min.js.gz
var compressed []byte

var (
	once   sync.Once
	script string
)

// Script is mermaid.min.js: it sets the global mermaid.
func Script() string {
	once.Do(func() {
		r, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			panic("mermaid: the embedded script is not gzip: " + err.Error())
		}
		b, err := io.ReadAll(r)
		if err != nil {
			panic("mermaid: the embedded script does not unpack: " + err.Error())
		}
		script = string(b)
	})
	return script
}

// Hash is a script's digest as a Content-Security-Policy source: 'sha256-…', the quotes included.
func Hash(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}
