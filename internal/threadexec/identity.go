package threadexec

import (
	"encoding/hex"
	"strings"
)

// CanonicalID matches Codex's UUID text forms for local execution
// correlation. Other IDs are opaque. The caller's wire value and public
// fallback ID keep their original spelling.
func CanonicalID(id string) string {
	// A bare lowercase hyphenated UUID already has canonical spelling.
	// Opaque 36-byte IDs that need no transformation keep it too.
	if len(id) == 36 && !strings.ContainsAny(id, "ABCDEF") {
		return id
	}
	value := id
	switch {
	case len(id) == 38 && id[0] == '{' && id[37] == '}':
		value = id[1:37]
	case len(id) == 45 && strings.HasPrefix(id, "urn:uuid:"):
		value = id[9:]
	}
	digits := value
	switch len(value) {
	case 32:
	case 36:
		if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
			return id
		}
		digits = value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	default:
		return id
	}
	var decoded [16]byte
	if _, err := hex.Decode(decoded[:], []byte(digits)); err != nil {
		return id
	}
	digits = strings.ToLower(digits)
	return digits[:8] + "-" + digits[8:12] + "-" + digits[12:16] + "-" + digits[16:20] + "-" + digits[20:]
}
