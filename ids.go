package golem

import (
	"crypto/rand"
	"fmt"
	"io"
	"time"
)

// NewID mints a unique, time-ordered identifier in UUID version 7 form
// (RFC 9562): a millisecond timestamp followed by cryptographically
// secure random bytes, so identifiers sort by creation time. Runs mint
// their own run identifiers and, when history carries none, their
// conversation identifiers; NewID is what an application uses to mint
// its own — pinning a run's identity to a trace ID it already has, or
// forking a conversation by supplying a fresh identifier to
// WithConversationID. Uniqueness within one millisecond rests on the
// random bits, so simultaneous mints are unordered but distinct.
func NewID() string {
	return uuid7At(time.Now().UnixMilli(), rand.Reader)
}

// uuid7At builds one identifier from a Unix millisecond timestamp and a
// random source, so tests can pin both. crypto/rand.Reader never
// returns an error on supported platforms; a failure there is the same
// unrecoverable entropy exhaustion the standard library itself panics
// on, so this reports it the same way rather than minting a
// predictable identifier.
func uuid7At(unixMilli int64, random io.Reader) string {
	var b [16]byte
	b[0] = byte(unixMilli >> 40)
	b[1] = byte(unixMilli >> 32)
	b[2] = byte(unixMilli >> 24)
	b[3] = byte(unixMilli >> 16)
	b[4] = byte(unixMilli >> 8)
	b[5] = byte(unixMilli)
	if _, err := io.ReadFull(random, b[6:]); err != nil {
		panic(fmt.Sprintf("golem: minting an identifier failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
