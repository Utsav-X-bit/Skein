package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// notesKey installs a throwaway release key and returns its private half.
func notesKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := updateVerifyKeys
	updateVerifyKeys = map[string]ed25519.PublicKey{updateKeyID: pub}
	t.Cleanup(func() { updateVerifyKeys = old })
	return priv
}

// notesManifest stages a binary and writes a manifest whose notes signature
// covers signedNotes while the manifest carries storedNotes. Passing sign=false
// omits the notes signature.
func notesManifest(t *testing.T, priv ed25519.PrivateKey, staging, signedNotes, storedNotes string, sign bool) string {
	t.Helper()
	if err := os.Chmod(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(staging, expectedBinaryName())
	if err := os.WriteFile(staged, []byte("new-skeind-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("new-skeind-bytes"))
	m := UpdateManifest{
		Version:    "0.2.0",
		Binary:     expectedBinaryName(),
		SHA256:     hex.EncodeToString(sum[:]),
		KeyID:      updateKeyID,
		ValidUntil: time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	m = signManifest(t, priv, m)
	m.ReleaseNotes = storedNotes
	if sign {
		digest := sha256.Sum256([]byte(signedNotes))
		m.NotesSignature = base64.StdEncoding.EncodeToString(
			ed25519.Sign(priv, []byte(hex.EncodeToString(digest[:]))))
	}
	return writeManifest(t, staging, m)
}

func TestUpdateCheckLegacyManifestStillVerifies(t *testing.T) {
	d, _ := newSystemDaemon(t)
	staging := updateStagingDir(t)
	d.SetUpdateStagingDir(staging)
	manifest, _ := newSignedManifest(t, staging, "0.2.0")
	res, err := d.UpdateCheck(context.Background(), manifest)
	if err != nil {
		t.Fatalf("legacy manifest: %v", err)
	}
	if res.ReleaseNotes != "" {
		t.Fatalf("legacy release notes = %q, want empty", res.ReleaseNotes)
	}
}

func TestUpdateCheckReturnsVerifiedReleaseNotes(t *testing.T) {
	d, _ := newSystemDaemon(t)
	staging := updateStagingDir(t)
	d.SetUpdateStagingDir(staging)
	priv := notesKey(t)
	notes := "Security fixes\n- pinned relay identity\n"
	manifest := notesManifest(t, priv, staging, notes, notes, true)
	res, err := d.UpdateCheck(context.Background(), manifest)
	if err != nil {
		t.Fatalf("signed notes: %v", err)
	}
	if res.ReleaseNotes != notes {
		t.Fatalf("release notes = %q, want %q", res.ReleaseNotes, notes)
	}
}

func TestUpdateCheckRejectsBadNotesSignatures(t *testing.T) {
	d, _ := newSystemDaemon(t)
	staging := updateStagingDir(t)
	d.SetUpdateStagingDir(staging)
	priv := notesKey(t)

	cases := []struct {
		name        string
		signedNotes string
		storedNotes string
		sign        bool
	}{
		{name: "missing", signedNotes: "notes", storedNotes: "notes", sign: false},
		{name: "tampered", signedNotes: "notes", storedNotes: "notes plus tampering", sign: true},
		{name: "wrong-payload", signedNotes: "other notes", storedNotes: "notes", sign: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := notesManifest(t, priv, staging, tc.signedNotes, tc.storedNotes, tc.sign)
			if _, err := d.UpdateCheck(context.Background(), manifest); err == nil {
				t.Fatalf("%s notes accepted", tc.name)
			}
		})
	}
}

func TestUpdateCheckRejectsOversizedReleaseNotes(t *testing.T) {
	d, _ := newSystemDaemon(t)
	staging := updateStagingDir(t)
	d.SetUpdateStagingDir(staging)
	priv := notesKey(t)
	notes := strings.Repeat("a", (64<<10)+1)
	manifest := notesManifest(t, priv, staging, notes, notes, true)
	_, err := d.UpdateCheck(context.Background(), manifest)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized notes error = %v, want a limit rejection", err)
	}
}

func TestNotesPayloadIsHashedNotes(t *testing.T) {
	m := UpdateManifest{ReleaseNotes: "hello"}
	sum := sha256.Sum256([]byte("hello"))
	if got, want := m.NotesPayload(), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("NotesPayload = %q, want %q", got, want)
	}
}
