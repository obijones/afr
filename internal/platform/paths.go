// Package platform holds the small set of OS differences afr depends on:
// where data lives and how files are made owner-only.
package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// DataDir returns afr's per-user data directory. AFR_HOME overrides it,
// which keeps development and test captures out of the real log.
//
//	Linux:   $XDG_DATA_HOME/afr, else ~/.local/share/afr
//	Windows: %LOCALAPPDATA%\afr
func DataDir() (string, error) {
	if d := os.Getenv("AFR_HOME"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		d := os.Getenv("LOCALAPPDATA")
		if d == "" {
			return "", errors.New("LOCALAPPDATA is not set")
		}
		return filepath.Join(d, "afr"), nil
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "afr"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "afr"), nil
}

// MkdirPrivate creates dir and its parents, readable only by the owner.
// On Windows the mode bits are ignored; owner-only ACLs are a planned
// addition (see the cross-platform table in the build plan).
func MkdirPrivate(dir string) error {
	return os.MkdirAll(dir, 0o700)
}

// WriteFilePrivate writes data to a new file readable only by the owner.
// It fails if the file already exists, so two captures never overwrite
// each other.
func WriteFilePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// AppendPrivate appends data to path, creating it owner-only if needed.
func AppendPrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
