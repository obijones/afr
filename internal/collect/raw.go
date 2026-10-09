// Package collect turns harness hook payloads into afr records.
package collect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/obijones/afr/internal/platform"
)

// rawMeta is the minimum read from a payload to file it. Everything else
// is saved verbatim.
type rawMeta struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
}

// SaveRaw writes one hook payload, byte for byte, to
// <dataDir>/raw/<session>/<time>-<pid>-<event>.json and returns the path.
// The payload need not be valid JSON; unparseable input is still kept,
// under "unknown-session".
func SaveRaw(dataDir string, payload []byte, now time.Time) (string, error) {
	var m rawMeta
	_ = json.Unmarshal(payload, &m) // best effort; the raw bytes are saved either way

	dir := filepath.Join(dataDir, "raw", safeName(m.SessionID, "unknown-session"))
	if err := platform.MkdirPrivate(dir); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%09d-%d-%s.json",
		now.UTC().Format("20060102T150405"), now.Nanosecond(), os.Getpid(),
		safeName(m.HookEventName, "unknown-event"))
	path := filepath.Join(dir, name)
	return path, platform.WriteFilePrivate(path, payload)
}

// safeName keeps a payload-supplied value usable as a single path element.
func safeName(s, fallback string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 128 {
			break
		}
	}
	out := b.String()
	if out == "" || strings.Trim(out, ".") == "" {
		return fallback
	}
	return out
}
