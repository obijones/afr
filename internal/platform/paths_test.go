package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDataDirOverride(t *testing.T) {
	t.Setenv("AFR_HOME", "/tmp/afr-test-home")
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/afr-test-home" {
		t.Fatalf("DataDir() = %q, want AFR_HOME value", got)
	}
}

func TestDataDirXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG applies to Linux only")
	}
	t.Setenv("AFR_HOME", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/xdg", "afr"); got != want {
		t.Fatalf("DataDir() = %q, want %q", got, want)
	}
}

func TestWriteFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := WriteFilePrivate(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFilePrivate(path, []byte("y")); err == nil {
		t.Fatal("second write to the same path succeeded, want error")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("mode = %o, want 600", perm)
		}
	}
}
