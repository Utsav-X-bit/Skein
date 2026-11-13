package path

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func TestNativeLocalCandidatesAreFilteredAndDeduplicated(t *testing.T) {
	old := LocalAddresses
	t.Cleanup(func() { LocalAddresses = old })
	LocalAddresses = func() ([]netip.Addr, error) {
		return []netip.Addr{
			netip.MustParseAddr("192.0.2.2"),
			netip.MustParseAddr("::ffff:192.0.2.2"),
			netip.MustParseAddr("2001:db8::1"),
			netip.MustParseAddr("127.0.0.1"),
			netip.MustParseAddr("fe80::1"),
		}, nil
	}
	got, err := LocalCandidates(11010)
	want := []netip.AddrPort{netip.MustParseAddrPort("192.0.2.2:11010"), netip.MustParseAddrPort("[2001:db8::1]:11010")}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("native candidates = %v, %v, want %v", got, err, want)
	}
	wantErr := errors.New("native inventory unavailable")
	LocalAddresses = func() ([]netip.Addr, error) { return nil, wantErr }
	if _, err := LocalCandidates(11010); !errors.Is(err, wantErr) {
		t.Fatalf("native inventory failure was hidden: %v", err)
	}
}
