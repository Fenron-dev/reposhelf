// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"crypto/rand"
	"time"
)

// NewID returns a new ULID (48-bit millisecond timestamp, 80 random bits,
// Crockford base32). IDs sort by creation time and stay stable when a
// repository is renamed (docs/SCHEMA.md).
func NewID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(b[6:]); err != nil {
		// crypto/rand does not fail on supported platforms (Go ≥ 1.24).
		panic(err)
	}
	// 128 bits → 26 characters of 5 bits; the first holds only 3 bits.
	var out [26]byte
	var acc uint64
	var bits uint
	pos := len(out) - 1
	for i := len(b) - 1; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 {
			out[pos] = ulidAlphabet[acc&0x1f]
			pos--
			acc >>= 5
			bits -= 5
		}
	}
	out[0] = ulidAlphabet[acc&0x1f]
	return string(out[:])
}
