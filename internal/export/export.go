// Package export packs recorded sessions into one zip for review on
// another machine: the logs, their state files, raw payloads, the error
// log, a rendered replay per session, and a manifest of file hashes.
//
// Inside the zip, files keep the data directory's layout, so pointing
// AFR_HOME at the unzipped folder lets afr replay and verify them as is.
package export

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/obijones/afr/internal/coverage"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/view"
)

// Options controls what goes into an export.
type Options struct {
	Version string    // afr version, recorded in the manifest
	Now     time.Time // export time, recorded in the manifest
	Width   int       // replay summary width; 0 means 200

	// Coverage, when not nil, is written as coverage.json: what Claude
	// Code's transcripts on this machine say afr missed. The transcripts
	// themselves are not exported.
	Coverage    []coverage.Result
	CoverageDir string
}

// coverageFile is coverage.json in the zip.
type coverageFile struct {
	TranscriptsDir string            `json:"transcripts_dir"`
	Note           string            `json:"note"`
	Results        []coverage.Result `json:"results"`
}

// Session is what the export found for one session.
type Session struct {
	Session   string `json:"session"`
	Status    string `json:"status"`    // "open" or "ended (reason)"
	Integrity string `json:"integrity"` // "ok" or a count of problems
	Events    int    `json:"events"`
	RawFiles  int    `json:"raw_files"`
}

// File is one manifest entry.
type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Manifest is written last, as manifest.json at the zip root.
type Manifest struct {
	AFRVersion string    `json:"afr_version"`
	ExportedAt time.Time `json:"exported_at"`
	OS         string    `json:"os"`
	Sessions   []Session `json:"sessions"`
	Files      []File    `json:"files"`
	Notes      []string  `json:"notes,omitempty"`
}

// Write packs the sessions whose logs are at logPaths into a zip on w.
// Each log and its state file are read together under the session lock,
// which is held only for that read, so a live session keeps recording.
func Write(w io.Writer, dataDir string, logPaths []string, o Options) (*Manifest, error) {
	if o.Width == 0 {
		o.Width = 200
	}
	m := &Manifest{AFRVersion: o.Version, ExportedAt: o.Now.UTC(), OS: runtime.GOOS}
	zw := zip.NewWriter(w)
	add := func(name string, modified time.Time, r io.Reader) error {
		hw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(hw, h), r)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		m.Files = append(m.Files, File{Path: name, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		return nil
	}
	addFile := func(name, src string) error {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		var mod time.Time
		if fi, err := f.Stat(); err == nil {
			mod = fi.ModTime()
		}
		return add(name, mod, f)
	}

	for _, lp := range logPaths {
		base := filepath.Base(lp)
		id := strings.TrimSuffix(base, ".ndjson")
		log, state, err := store.Snapshot(lp)
		if err != nil {
			return nil, fmt.Errorf("session %s: %w", id, err)
		}
		events, err := view.Parse(bytes.NewReader(log))
		if err != nil {
			return nil, fmt.Errorf("session %s: %w", id, err)
		}
		s := Session{Session: id, Status: view.Summarize(events).Status, Events: len(events), Integrity: "ok"}
		if probs, err := store.VerifyReader(bytes.NewReader(log)); err != nil {
			s.Integrity = "not checked (" + err.Error() + ")"
		} else if len(probs) > 0 {
			s.Integrity = fmt.Sprintf("%d problems, run afr verify", len(probs))
		}

		// Zip entry names always use forward slashes, whatever the OS.
		if err := add(path.Join("sessions", base), o.Now, bytes.NewReader(log)); err != nil {
			return nil, err
		}
		if state != nil {
			if err := add(path.Join("sessions", id+".state.json"), o.Now, bytes.NewReader(state)); err != nil {
				return nil, err
			}
		}
		var replay bytes.Buffer
		opts := view.Options{Full: true, Width: o.Width, Current: view.Rescan(events, dataDir)}
		if err := view.Replay(&replay, events, s.Integrity, opts); err != nil {
			return nil, fmt.Errorf("session %s: replay: %w", id, err)
		}
		if err := add("replay-"+short(id)+".txt", o.Now, &replay); err != nil {
			return nil, err
		}

		// Raw payloads are written once and never changed, so they need
		// no lock.
		rawDir := filepath.Join(dataDir, "raw", id)
		err = filepath.WalkDir(rawDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(rawDir, p)
			if err != nil {
				return err
			}
			s.RawFiles++
			return addFile(path.Join("raw", id, filepath.ToSlash(rel)), p)
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("session %s: raw payloads: %w", id, err)
		}
		m.Sessions = append(m.Sessions, s)
	}

	if o.Coverage != nil {
		cb, err := json.MarshalIndent(coverageFile{
			TranscriptsDir: o.CoverageDir, Results: o.Coverage,
			Note: "advisory: compared with Claude Code transcripts, which are not tamper-evident and are deleted after about 30 days",
		}, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := add("coverage.json", o.Now, bytes.NewReader(append(cb, '\n'))); err != nil {
			return nil, err
		}
	}

	errLog := filepath.Join(dataDir, "afr-errors.log")
	if _, err := os.Stat(errLog); err == nil {
		if err := addFile("afr-errors.log", errLog); err != nil {
			return nil, err
		}
		m.Notes = append(m.Notes, "afr-errors.log is the whole error log, not only these sessions")
	}

	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	hw, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate, Modified: o.Now})
	if err != nil {
		return nil, err
	}
	if _, err := hw.Write(append(mb, '\n')); err != nil {
		return nil, err
	}
	return m, zw.Close()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
