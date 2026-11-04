//go:build windows

package fileperm

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// getSecurityDescriptorDaclProc is advapi32's GetSecurityDescriptorDacl:
// x/sys/windows wraps GetNamedSecurityInfo but not the DACL accessor.
var getSecurityDescriptorDaclProc = windows.NewLazySystemDLL("advapi32.dll").NewProc("GetSecurityDescriptorDacl")

func getDacl(sd *windows.SECURITY_DESCRIPTOR) (*windows.ACL, bool, error) {
	var present uint32
	var dacl *windows.ACL
	var defaulted uint32
	r1, _, errno := getSecurityDescriptorDaclProc.Call(
		uintptr(unsafe.Pointer(sd)),
		uintptr(unsafe.Pointer(&present)),
		uintptr(unsafe.Pointer(&dacl)),
		uintptr(unsafe.Pointer(&defaulted)),
	)
	if r1 == 0 {
		if errno != nil {
			return nil, false, errno
		}
		return nil, false, fmt.Errorf("GetSecurityDescriptorDacl failed")
	}
	return dacl, present != 0, nil
}

// walkDacl calls fn for each ACE of path's DACL, in order, while the
// security descriptor is alive. It reports whether a DACL is present
// at all: a missing DACL means full access for everyone. fn returns
// false to stop early. ACE SIDs point into the descriptor buffer and
// must not be retained past the walk.
func walkDacl(path string, fn func(aceType uint8, mask windows.ACCESS_MASK, sid *windows.SID) bool) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	defer runtime.KeepAlive(sd)
	dacl, present, err := getDacl(sd)
	if err != nil {
		return false, err
	}
	if !present || dacl == nil {
		return false, nil
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, err
		}
		if !fn(ace.Header.AceType, ace.Mask, (*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			break
		}
	}
	return true, nil
}

// writeAccessMask is every right that lets a holder modify a directory
// or its contents: data/attribute writes, child deletion, deletion,
// and DACL/owner changes, including the generic aliases.
const writeAccessMask windows.ACCESS_MASK = 0x0002 | // FILE_WRITE_DATA
	0x0004 | // FILE_APPEND_DATA
	0x0010 | // FILE_WRITE_EA
	0x0040 | // FILE_DELETE_CHILD
	0x0100 | // FILE_WRITE_ATTRIBUTES
	0x00010000 | // DELETE
	0x00040000 | // WRITE_DAC
	0x00080000 | // WRITE_OWNER
	0x40000000 | // GENERIC_WRITE
	0x10000000 // GENERIC_ALL

// adminSIDs are the principals allowed to write a daemon parent:
// SYSTEM, Administrators, and TrustedInstaller (best effort —
// %ProgramFiles% takes a separate fast path, so a missing TI SID
// only makes non-package parents fail closed).
func adminSIDs() ([]*windows.SID, error) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	sids := []*windows.SID{system, admins}
	if u16, err := windows.UTF16FromString("S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"); err == nil {
		var ti *windows.SID
		if err := windows.ConvertStringSidToSid(&u16[0], &ti); err == nil {
			sids = append(sids, ti)
		}
	}
	return sids, nil
}

// dirAdminOnly reports whether dir's DACL grants write-like access
// only to administrators. Evaluation follows Windows order: for each
// untrusted SID and each write bit, the first ACE mentioning that
// SID and bit decides (deny wins over a later allow). Read and
// execute rights for anyone are fine, as they are under
// %ProgramFiles% itself. An empty DACL is not admin-only: it denies
// even the service, so the directory could not host a daemon.
func dirAdminOnly(dir string) (bool, error) {
	admins, err := adminSIDs()
	if err != nil {
		return false, err
	}
	type sidState struct {
		sid    *windows.SID
		denied windows.ACCESS_MASK
	}
	var states []*sidState
	aces := 0
	failed := false
	present, err := walkDacl(dir, func(aceType uint8, mask windows.ACCESS_MASK, sid *windows.SID) bool {
		aces++
		if failed {
			return false
		}
		for _, a := range admins {
			if windows.EqualSid(sid, a) {
				return true
			}
		}
		var st *sidState
		for _, s := range states {
			if windows.EqualSid(sid, s.sid) {
				st = s
				break
			}
		}
		if st == nil {
			st = &sidState{sid: sid}
			states = append(states, st)
		}
		bits := mask & writeAccessMask
		if bits == 0 {
			return true
		}
		switch aceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			st.denied |= bits
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if bits&^st.denied != 0 {
				failed = true
				return false
			}
		}
		return true
	})
	if err != nil {
		return false, err
	}
	if !present || aces == 0 || failed {
		return false, nil
	}
	return true, nil
}

// noWorldAccess reports whether no well-known non-administrator
// principal (Everyone, Authenticated Users, Users) has any access to
// path. The file's owner and administrators may have access.
func noWorldAccess(path string) (bool, error) {
	var blocked []*windows.SID
	for _, id := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,
		windows.WinAuthenticatedUserSid,
		windows.WinBuiltinUsersSid,
	} {
		sid, err := windows.CreateWellKnownSid(id)
		if err != nil {
			return false, err
		}
		blocked = append(blocked, sid)
	}
	violated := false
	present, err := walkDacl(path, func(aceType uint8, mask windows.ACCESS_MASK, sid *windows.SID) bool {
		if aceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return true
		}
		for _, b := range blocked {
			if windows.EqualSid(sid, b) {
				violated = true
				return false
			}
		}
		return true
	})
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	return !violated, nil
}
