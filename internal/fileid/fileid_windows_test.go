//go:build windows

package fileid

import "testing"

// An identifier all zeros or all ones is no identity (MS-FSCC): refused rather than taken, since two
// files could share it; any other is taken.
func TestASentinelFileIDIsNoIdentity(t *testing.T) {
	var zero, ones, some [16]byte
	for i := range ones {
		ones[i] = 0xff
	}
	some[3] = 7
	if !sentinel(zero) || !sentinel(ones) {
		t.Error("an all-zeros or all-ones identifier was taken as an identity")
	}
	if sentinel(some) {
		t.Error("an ordinary identifier was refused")
	}
}

// An id carries the identifier's 128 bits whole: two identifiers equal in their low 64 bits but not
// their high are two ids (the legacy 64-bit index would have made them one), and two volumes' same
// identifier are two ids too.
func TestAnIDCarriesTheWholeIdentifier(t *testing.T) {
	var lowOnly, highToo [16]byte
	lowOnly[0], highToo[0] = 1, 1
	highToo[15] = 1 // the same low 64 bits, a different high 64 (FILE_ID_128 is little-endian bytes)
	a, errA := format(7, lowOnly)
	b, errB := format(7, highToo)
	if errA != nil || errB != nil || a == b {
		t.Errorf("ids differing only in the identifier's high 64 bits: %q, %q (%v, %v); want two ids", a, b, errA, errB)
	}
	c, errC := format(8, lowOnly)
	if errC != nil || c == a {
		t.Errorf("one identifier on two volumes: %q and %q (%v); want two ids", a, c, errC)
	}
	if _, err := format(7, [16]byte{}); err == nil {
		t.Error("a sentinel identifier was formatted as an id")
	}
}
