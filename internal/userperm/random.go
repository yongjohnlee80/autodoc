package userperm

import (
	"crypto/rand"
	"encoding/binary"
)

// random is a name's random part.
func random() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}
