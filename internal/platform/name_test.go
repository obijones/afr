package platform

import (
	"strings"
	"testing"
)

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"abc-123_X.y":   "abc-123_X.y",
		"../../etc":     ".._.._etc",
		"..":            "fb",
		"":              "fb",
		"a/b\\c":        "a_b_c",
		"con:aux?*":     "con_aux__",
		"3021b5c9-9be8": "3021b5c9-9be8",
	}
	for in, want := range cases {
		if got := SafeFileName(in, "fb"); got != want {
			t.Errorf("SafeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeFileName(strings.Repeat("a", 300), "fb"); len(got) != 128 {
		t.Errorf("long name kept %d bytes, want 128", len(got))
	}
}
