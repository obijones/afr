package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/obijones/afr/internal/collect"
	"github.com/obijones/afr/internal/platform"
)

const maxPayload = 32 << 20 // 32 MiB; larger payloads are cut and logged

// stdinTimeout bounds how long the hook waits for its payload, so a
// missing or stuck stdin can never hold up the agent. A var for tests.
var stdinTimeout = 5 * time.Second

// runHook is the non-interference rule in code: it always returns 0 and
// never writes to stdout. Claude Code reads exit 2 from PreToolUse as a
// block and stdout JSON as a decision; Copilot CLI denies the tool on any
// non-zero PreToolUse exit. Every failure goes to afr's own error log.
//
// The recover covers only this goroutine. Any goroutine started on the
// hook path must not panic, or the process still exits 2.
func runHook(stdin io.Reader) (code int) {
	defer func() {
		if r := recover(); r != nil {
			logError(fmt.Sprintf("panic: %v\n%s", r, debug.Stack()))
			code = 0
		}
	}()

	if os.Getenv("AFR_TEST_PANIC") == "1" {
		panic("forced panic (AFR_TEST_PANIC=1)")
	}

	payload, err := readWithTimeout(stdin, stdinTimeout, maxPayload)
	if err != nil {
		logError("read stdin: " + err.Error())
	}
	if len(payload) == 0 {
		return 0
	}

	dataDir, err := platform.DataDir()
	if err != nil {
		logError("data dir: " + err.Error())
		return 0
	}
	if _, err := collect.SaveRaw(dataDir, payload, time.Now()); err != nil {
		logError("save raw payload: " + err.Error())
	}
	return 0
}

// readWithTimeout reads r until EOF, max bytes, or timeout, whichever
// comes first, and returns what it has.
func readWithTimeout(r io.Reader, timeout time.Duration, max int64) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(r, max+1))
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		if int64(len(res.b)) > max {
			return res.b[:max], fmt.Errorf("payload larger than %d bytes, truncated", max)
		}
		return res.b, res.err
	case <-time.After(timeout):
		return nil, errors.New("timed out waiting for stdin")
	}
}

// logError appends a timestamped line to <dataDir>/afr-errors.log. It
// fails silently: there is nowhere safe left to report to.
func logError(msg string) {
	dataDir, err := platform.DataDir()
	if err != nil || platform.MkdirPrivate(dataDir) != nil {
		return
	}
	line := fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), msg)
	_ = platform.AppendPrivate(filepath.Join(dataDir, "afr-errors.log"), []byte(line))
}
