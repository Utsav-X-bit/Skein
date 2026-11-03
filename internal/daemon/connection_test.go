package daemon

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

func TestLogRingPaginates(t *testing.T) {
	r := &logRing{}
	for range 5 {
		r.add("info", "line")
	}
	got, next := r.tail(0, 2)
	if len(got) != 2 || next != 2 {
		t.Fatalf("tail(0,2) = %d entries, next=%d", len(got), next)
	}
	got, next = r.tail(2, 10)
	if len(got) != 3 || next != 5 {
		t.Fatalf("tail(2,10) = %d entries, next=%d", len(got), next)
	}
	got, next = r.tail(5, 10)
	if len(got) != 0 || next != 5 {
		t.Fatalf("tail(5,10) = %d entries, next=%d", len(got), next)
	}
}

func TestLogRingWraps(t *testing.T) {
	r := &logRing{}
	for range logRingSize + 10 {
		r.add("info", "line")
	}
	got, next := r.tail(0, 100000)
	if len(got) != logRingSize {
		t.Fatalf("ring holds %d entries, want %d", len(got), logRingSize)
	}
	if next != uint64(logRingSize+10) {
		t.Fatalf("next = %d, want %d", next, logRingSize+10)
	}
}

func TestRedactorScrubsSecrets(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte('a' + i%26)
	}
	cfg := conf.Default()
	cfg.Node.NetworkSecret = secret
	cfg.API.Token = []byte("token-token-token")
	r := newRedactor(cfg)

	b64 := base64.RawURLEncoding.EncodeToString(secret)
	if out := r.apply("using " + b64); strings.Contains(out, b64) {
		t.Fatalf("secret leaked: %s", out)
	}
	if out := r.apply("join with skein:2:abcDEF_123 now"); !strings.Contains(out, "skein:<redacted>") {
		t.Fatalf("join string leaked: %s", out)
	}
	if out := r.apply("plain message"); out != "plain message" {
		t.Fatalf("over-redaction: %s", out)
	}
	if list := r.list(); len(list) == 0 || list[0] != "join_strings" {
		t.Fatalf("redactions list = %v", list)
	}
}

func TestLogWriterParsesLevels(t *testing.T) {
	w := logWriter{ring: &logRing{}, red: newRedactor(conf.Default())}
	if _, err := w.Write([]byte("2026/11/03 10:00:00 skein: warning: TCP fallback unavailable on port 11010; UDP only\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("2026/11/03 10:00:01 skein: error: API listen: bind failed\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("2026/11/03 10:00:02 skein: plain\n")); err != nil {
		t.Fatal(err)
	}
	entries, _ := w.ring.tail(0, 10)
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Level != "warning" || entries[1].Level != "error" || entries[2].Level != "info" {
		t.Fatalf("levels = %v %v %v", entries[0].Level, entries[1].Level, entries[2].Level)
	}
	if strings.Contains(entries[0].Message, "skein:") {
		t.Fatalf("prefix kept: %s", entries[0].Message)
	}
}

func TestDisconnectOnPausedConnectionDoesNotDeadlock(t *testing.T) {
	d := &Daemon{conn: &conn{state: ConnPaused, since: time.Now()}}
	done := make(chan error, 1)
	go func() {
		_, err := d.Disconnect(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Disconnect on a paused connection returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect deadlocked while reading the paused state")
	}
}

func TestConnectOnConnectedConnectionDoesNotDeadlock(t *testing.T) {
	d := &Daemon{conn: &conn{state: ConnConnected, since: time.Now()}}
	done := make(chan error, 1)
	go func() {
		_, err := d.Connect(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Connect on a connected connection returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("Connect deadlocked while reading the connected state")
	}
}
