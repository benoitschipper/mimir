//go:build internlabels

package labels

import (
	"github.com/cespare/xxhash/v2"
)

// StableHash is a labels hashing implementation which is guaranteed to not change over time.
// This function should be used whenever labels hashing backward compatibility must be guaranteed.
func StableHash(ls Labels) uint64 {
	// Use xxhash.Sum64(b) for fast path as it's faster.
	b := make([]byte, 0, 1024)
	for pos := 0; pos < len(ls.data); {
		name, newPos := decodeString(ls.syms, ls.data, pos)
		value, newPos := decodeString(ls.syms, ls.data, newPos)
		if len(b)+len(name)+len(value)+2 >= cap(b) {
			// If labels entry is 1KB+, hash the rest of them via Write().
			h := xxhash.New()
			_, _ = h.Write(b)
			for pos < len(ls.data) {
				name, pos = decodeString(ls.syms, ls.data, pos)
				value, pos = decodeString(ls.syms, ls.data, pos)
				_, _ = h.WriteString(name)
				_, _ = h.Write(seps)
				_, _ = h.WriteString(value)
				_, _ = h.Write(seps)
			}
			return h.Sum64()
		}

		b = append(b, name...)
		b = append(b, seps[0])
		b = append(b, value...)
		b = append(b, seps[0])
		pos = newPos
	}
	return xxhash.Sum64(b)
}
