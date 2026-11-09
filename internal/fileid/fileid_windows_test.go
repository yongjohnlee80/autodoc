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
