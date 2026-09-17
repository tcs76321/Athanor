// Package ids generates application-side identifiers. All persisted IDs
// are UUIDs (ARCHITECTURE §5: "IDs are application-generated UUIDs"), so
// every table shares one generator instead of each package rolling its
// own.
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a random (version 4, variant 2) UUID string such as
// "3b241101-e2bb-4255-8caf-4136c566a962". Generation cannot fail in
// practice on supported platforms; a broken crypto/rand panics loudly
// rather than handing out duplicate IDs.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ids: crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}
