//go:build windows

package fileid

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileIDInfo is FILE_ID_INFO: the volume's 64-bit serial and the file's 128-bit identifier, which
// GetFileInformationByHandleEx(FileIdInfo) fills. Unlike BY_HANDLE_FILE_INFORMATION's 32-bit serial
// and 64-bit index, it is unique on ReFS too.
type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

// errNoIdentity is a file system that gives the file no unique identity: an identifier all zeros or
// all ones (MS-FSCC). The store refuses it rather than take a value two files could share.
var errNoIdentity = errors.New("the file system gives the file no unique identity")

// id is the file's volume serial and 128-bit file identifier, as "serial-identifier" in hex, from
// GetFileInformationByHandleEx(FileIdInfo): the same through a hardlink and, the symlink followed,
// through a symlink.
func id(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// no access but its attributes, sharing everything, and a folder too (BACKUP_SEMANTICS)
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var info fileIDInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return "", fmt.Errorf("its file identifier: %w", err)
	}
	return format(info.VolumeSerialNumber, info.FileID)
}

// format is a volume serial and a 128-bit file identifier as the id: "serial-identifier", each in
// hex, the identifier's 16 bytes whole (two identifiers that differ only in their high 64 bits are two
// ids); errNoIdentity for a sentinel identifier.
func format(serial uint64, fid [16]byte) (string, error) {
	if sentinel(fid) {
		return "", errNoIdentity
	}
	return fmt.Sprintf("%016x-%x", serial, fid), nil
}

// sentinel is whether an identifier is all zeros or all ones: MS-FSCC's "no unique identity".
func sentinel(b [16]byte) bool {
	zero, ones := true, true
	for _, c := range b {
		zero = zero && c == 0
		ones = ones && c == 0xff
	}
	return zero || ones
}
