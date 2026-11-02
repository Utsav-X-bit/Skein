package wire

import "testing"

func BenchmarkParse(b *testing.B) {
	frame, err := Marshal(Header{Type: Data, ToNext: 1, ToFinal: 2, From: 3, TTL: MaxTTL}, make([]byte, 1024))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := Parse(frame); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal(b *testing.B) {
	payload := make([]byte, 1024)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(Header{Type: Data, ToNext: 1, ToFinal: 2, From: 3, TTL: MaxTTL}, payload); err != nil {
			b.Fatal(err)
		}
	}
}
