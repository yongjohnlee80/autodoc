package mermaid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// wantDigest is mermaid.min.js's sha256, from the registry-checked tarball.
const wantDigest = "6484afc32872a3aa16cac9a76ba1816a1ed4cc870a6593cc2e17757750f518b2"

// The embedded script is mermaid 12.1.0's mermaid.min.js, byte for byte, and sets the global.
func TestTheVendoredScript(t *testing.T) {
	s := Script()
	sum := sha256.Sum256([]byte(s))
	if got := hex.EncodeToString(sum[:]); got != wantDigest {
		t.Fatalf("the embedded script's sha256 is %s, want %s (mermaid %s's dist/mermaid.min.js)", got, wantDigest, Version)
	}
	if !strings.Contains(s, `globalThis["mermaid"]`) {
		t.Fatal("the script does not set the global mermaid")
	}
	if Script() != s {
		t.Fatal("a second call gave another script")
	}
}

func TestHash(t *testing.T) {
	if got := Hash("alert(1)"); got != "'sha256-bhHHL3z2vDgxUt0W3dWQOrprscmda2Y5pLsLg4GF+pI='" {
		t.Fatalf("Hash = %s", got)
	}
}
