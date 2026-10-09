package platform

import "strings"

// SafeFileName keeps an externally supplied value (a session id, an event
// name) usable as a single path element on Linux and Windows. Anything
// outside [A-Za-z0-9._-] becomes '_', the result is capped at 128 bytes,
// and values that are empty or only dots return fallback.
func SafeFileName(s, fallback string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 128 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if strings.Trim(out, ".") == "" {
		return fallback
	}
	return out
}
