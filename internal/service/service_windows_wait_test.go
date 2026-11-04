//go:build windows

package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeSC installs a fake sc.bat at the front of PATH. The fake answers
// `sc stop` as directed and `sc query` with STOP_PENDING for the
// first pendingQueries calls, then STOPPED, counting queries in a
// file so the test can assert the wait actually polled. Lines are
// kept out of parenthesised blocks: cmd expands %n% per line, but
// inside a block it would read the pre-block value.
func fakeSC(t *testing.T, stopExit int, stopText string, pendingQueries int) (dir string, queryCount func() int) {
	t.Helper()
	dir = t.TempDir()
	bat := "@echo off\r\n" +
		"if \"%1\"==\"stop\" goto stop\r\n" +
		"if \"%1\"==\"query\" goto query\r\n" +
		"if \"%1\"==\"qc\" goto qc\r\n" +
		"exit /b 0\r\n" +
		":stop\r\n" +
		"echo " + stopText + "\r\n" +
		"exit /b " + strconv.Itoa(stopExit) + "\r\n" +
		":query\r\n" +
		"set n=0\r\n" +
		"if exist \"%~dp0count.txt\" for /f %%a in (%~dp0count.txt) do set n=%%a\r\n" +
		"set /a n=n+1\r\n" +
		// Redirect-first: `echo %n%>file` with a numeric n is parsed
		// as an fd redirect (`echo 1>file` prints "ECHO is off."
		// into the file), which silently wedged the counter.
		">\"%~dp0count.txt\" echo %n%\r\n" +
		"echo SERVICE_NAME: skein\r\n" +
		"if %n% LEQ " + strconv.Itoa(pendingQueries) + " echo         STATE              : 3  STOP_PENDING\r\n" +
		"if %n% GTR " + strconv.Itoa(pendingQueries) + " echo         STATE              : 1  STOPPED\r\n" +
		"exit /b 0\r\n" +
		":qc\r\n" +
		"echo [SC] QueryServiceConfig SUCCESS\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(filepath.Join(dir, "sc.bat"), []byte(bat), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, func() int {
		b, err := os.ReadFile(filepath.Join(dir, "count.txt"))
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return n
	}
}

// M9 §2c: a stop refused with 1061 (transition already in progress)
// must wait for STOPPED, not fail the upgrade.
func TestStopAndWaitTolerates1061(t *testing.T) {
	_, queries := fakeSC(t, 1, "[SC] ControlService FAILED 1061:", 2)
	if err := stopAndWait(); err != nil {
		t.Fatalf("stopAndWait with 1061 stop = %v, want nil", err)
	}
	if got := queries(); got < 3 {
		t.Fatalf("queries = %d, want >= 3 (wait must poll past STOP_PENDING)", got)
	}
}

// A clean stop still waits: stop() must not return while the service
// is STOP_PENDING, or the upgrade's binPath rewrite races it.
func TestStopAndWaitWaitsForStopped(t *testing.T) {
	_, queries := fakeSC(t, 0, "[SC] ControlService SUCCESS", 1)
	if err := stopAndWait(); err != nil {
		t.Fatalf("stopAndWait = %v, want nil", err)
	}
	if got := queries(); got < 2 {
		t.Fatalf("queries = %d, want >= 2 (must observe STOPPED, not just accept the control)", got)
	}
}
