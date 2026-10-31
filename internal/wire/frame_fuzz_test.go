package wire

import "testing"

func FuzzParse(f *testing.F) {
	seed, err := Marshal(Header{Type: Data, ToNext: 1, ToFinal: 2, From: 3, TTL: 4}, append(AppendNonce(nil, 1, 2), make([]byte, TagSize)...))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{})
	f.Add(make([]byte, MaxFrameSize))

	f.Fuzz(func(t *testing.T, b []byte) {
		h, payload, err := Parse(b)
		if err != nil {
			return
		}
		if len(b) < HeaderSize || len(b) > MaxFrameSize {
			t.Fatalf("accepted frame of %d bytes", len(b))
		}
		if len(payload) != len(b)-HeaderSize {
			t.Fatalf("payload %d does not match frame %d", len(payload), len(b))
		}
		if !h.Type.Valid() {
			t.Fatalf("accepted unknown type %d", h.Type)
		}
		// The payload must alias the input, never a slice outside it.
		if len(payload) > 0 && &payload[0] != &b[HeaderSize] {
			t.Fatal("payload does not alias the input frame")
		}
	})
}
