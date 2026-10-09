// Package store writes and reads afr's append-only session logs: one
// NDJSON file per session, each event chained to the one before it.
package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/schema"
)

// LockTimeout bounds the wait for a session's lock, so a stuck afr
// process can never stall later hook calls. A var for tests.
var LockTimeout = 2 * time.Second

// ErrLockTimeout means the session lock was not acquired in time; the
// caller should keep the payload some other way.
var ErrLockTimeout = errors.New("store: timed out waiting for session lock")

// SessionsDir is where session logs live under the data directory.
func SessionsDir(dataDir string) string { return filepath.Join(dataDir, "sessions") }

// LogPath is the log file for one session.
func LogPath(dataDir, session string) string {
	return filepath.Join(SessionsDir(dataDir), platform.SafeFileName(session, "unknown-session")+".ndjson")
}

// Append seals events in order and appends them to their session's log
// under the session lock. All events must belong to the same session.
func Append(dataDir string, events []schema.Event) error {
	if len(events) == 0 {
		return nil
	}
	return WithSession(dataDir, events[0].Session, func(w *Writer) error {
		return w.Append(events)
	})
}

// Writer appends to one session's log while its lock is held.
type Writer struct {
	f       *os.File
	session string
	seq     int64
	prev    string
	repair  bool
}

// WithSession holds the session's lock while fn runs, so concurrent hook
// processes never interleave writes or reuse a seq, and per-session state
// kept beside the log can be read and updated safely. The wait for the
// lock is bounded by LockTimeout.
func WithSession(dataDir, session string, fn func(w *Writer) error) error {
	if err := platform.MkdirPrivate(SessionsDir(dataDir)); err != nil {
		return err
	}
	path := LogPath(dataDir, session)

	// A separate lock file: on Windows a lock on the log itself would block
	// the write handle.
	lock := flock.New(path + ".lock")
	ctx, cancel := context.WithTimeout(context.Background(), LockTimeout)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("store: lock: %w", err)
	}
	if !ok {
		return ErrLockTimeout
	}
	defer lock.Unlock()

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	seq, prev, repair, err := tail(f)
	if err != nil {
		return err
	}
	return fn(&Writer{f: f, session: session, seq: seq, prev: prev, repair: repair})
}

// Seq is the seq of the last event in the log (0 for an empty log).
func (w *Writer) Seq() int64 { return w.seq }

// Append seals events (seq, prev_hash, hash) and writes them in one write.
func (w *Writer) Append(events []schema.Event) error {
	if len(events) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if w.repair {
		buf.WriteByte('\n') // end the damaged line so it cannot swallow ours
	}
	seq, prev := w.seq, w.prev
	for i := range events {
		if events[i].Session != w.session {
			return errors.New("store: events from more than one session")
		}
		seq++
		if err := events[i].Seal(seq, prev); err != nil {
			return err
		}
		prev = events[i].Hash
		line, err := json.Marshal(events[i])
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n') // always \n, never \r\n, on every platform
	}
	if _, err := w.f.Write(buf.Bytes()); err != nil {
		return err
	}
	w.seq, w.prev, w.repair = seq, prev, false
	return nil
}

// tail returns the seq and hash of the last complete event in f, and
// whether the file ends without a newline (a crash mid-write). A damaged
// last line is skipped; the next event chains to the last good one and
// Verify reports the damage.
func tail(f *os.File) (seq int64, hash string, needNewline bool, err error) {
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil || end == 0 {
		return 0, "", false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, end-1); err != nil {
		return 0, "", false, err
	}
	needNewline = last[0] != '\n'

	const chunk = 64 << 10
	var data []byte
	for pos := end; pos > 0; {
		n := min(int64(chunk), pos)
		pos -= n
		b := make([]byte, n)
		if _, err := f.ReadAt(b, pos); err != nil {
			return 0, "", false, err
		}
		data = append(b, data...)
		lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
		first := 0
		if pos > 0 {
			first = 1 // the window's first line may be cut off; read more before trusting it
		}
		for j := len(lines) - 1; j >= first; j-- {
			var e struct {
				Seq  int64  `json:"seq"`
				Hash string `json:"hash"`
			}
			if json.Unmarshal(lines[j], &e) == nil && e.Hash != "" {
				return e.Seq, e.Hash, needNewline, nil
			}
		}
	}
	return 0, "", needNewline, nil
}

// Problem is one integrity issue found by Verify.
type Problem struct {
	Line   int
	Detail string
}

// Verify reads a session log and reports seq gaps, broken hash links,
// hashes that do not match their event, content that does not match its
// content_hash, and unparseable lines.
func Verify(path string) ([]Problem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var probs []Problem
	var wantSeq int64 = 1
	prev := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for n := 1; sc.Scan(); n++ {
		var e schema.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			probs = append(probs, Problem{n, "unparseable line: " + err.Error()})
			continue
		}
		if e.Seq != wantSeq {
			probs = append(probs, Problem{n, fmt.Sprintf("seq %d, want %d", e.Seq, wantSeq)})
		}
		if e.PrevHash != prev {
			probs = append(probs, Problem{n, "prev_hash does not match the previous event"})
		}
		if h, err := e.ComputeHash(); err != nil || h != e.Hash {
			probs = append(probs, Problem{n, "hash does not match event contents"})
		}
		// Absent content is a retention purge, not damage; present content
		// must still match the content_hash the chain covers.
		if e.Content != nil {
			if h, err := e.ContentDigest(); err != nil || h != e.ContentHash {
				probs = append(probs, Problem{n, "content does not match content_hash"})
			}
		}
		wantSeq, prev = e.Seq+1, e.Hash
	}
	return probs, sc.Err()
}
