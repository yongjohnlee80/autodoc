package registrations

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"maps"
	"slices"
)

// Format is a deriver's identity for one format: its id and version (ADR 0216 §1.8).
type Format struct{ ID, Version string }

// Tables are a build's frozen registrations as a client compares them (ADR 0216 §1.4): a chunker's
// version by extension, and a deriver's identity by format. sys.capabilities reports the daemon's;
// a TUI's binary has its own.
type Tables struct {
	Chunkers map[string]string
	Formats  map[string]Format
}

// Tables are t's, empty for none.
func (t *Table) Tables() Tables {
	return Tables{Chunkers: t.Versions(), Formats: map[string]Format{}}
}

// Fingerprint is the hex sha256 over the entries, the chunkers then the formats, each sorted: a
// tag ('c' a chunker, 'd' a format) and its fields, every field length-prefixed (a uvarint, then
// its bytes). Nothing of the binary's bytes or build info goes in, so two builds with the same
// registrations agree, and the community build's empty tables hash the empty input.
func (t Tables) Fingerprint() string {
	var b []byte
	field := func(s string) {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}
	for _, ext := range slices.Sorted(maps.Keys(t.Chunkers)) {
		b = append(b, 'c')
		field(ext)
		field(t.Chunkers[ext])
	}
	for _, f := range slices.Sorted(maps.Keys(t.Formats)) {
		b = append(b, 'd')
		field(f)
		field(t.Formats[f].ID)
		field(t.Formats[f].Version)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// StrictSubsetOf reports whether every entry of t is in o, with the same version, and o has more:
// the only case in which a TUI built with o offers to restart a daemon reporting t, since versions
// have no order and a restart must never take registrations away.
func (t Tables) StrictSubsetOf(o Tables) bool {
	for ext, v := range t.Chunkers {
		if w, ok := o.Chunkers[ext]; !ok || w != v {
			return false
		}
	}
	for f, id := range t.Formats {
		if w, ok := o.Formats[f]; !ok || w != id {
			return false
		}
	}
	return len(t.Chunkers)+len(t.Formats) < len(o.Chunkers)+len(o.Formats)
}

// Lacks are the extensions and formats o registers that t does not, or registers otherwise,
// sorted: what a daemon reporting t would read otherwise than a build with o.
func (t Tables) Lacks(o Tables) []string {
	var out []string
	for ext, v := range o.Chunkers {
		if w, ok := t.Chunkers[ext]; !ok || w != v {
			out = append(out, ext)
		}
	}
	for f, id := range o.Formats {
		if w, ok := t.Formats[f]; !ok || w != id {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
