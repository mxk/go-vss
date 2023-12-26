//go:build windows

// Package vss exposes Windows Volume Shadow Copy API.
package vss

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

// errNotAdmin is returned when the current user lacks admin privileges.
var errNotAdmin = fmt.Errorf("vss: do not have Administrators group privileges (%w)",
	os.ErrPermission)

// Create creates a new shadow copy of the specified volume and returns its ID.
// The volume can be specified as a drive letter (e.g. "C:"), mount point, or
// a globally unique identifier (GUID) name (e.g. "\\?\Volume{GUID}\").
func Create(vol string) (string, error) {
	if !isAdmin() {
		return "", errNotAdmin
	}
	var id *ole.GUID
	err := wmiExec(func(s *sWbemServices) (err error) {
		id, err = create(s, vol)
		return
	})
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// CreateAt creates a new shadow copy and symlinks it at the specified path. The
// shadow copy is removed if symlinking fails.
func CreateAt(link, vol string) (err error) {
	id, err := Create(vol)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = Remove(id)
		}
	}()
	sc, _, err := get(id)
	if err != nil {
		return err
	}
	return sc.Link(link)
}

// Remove removes a shadow copy by ID, DeviceObject, or symlink path. If a valid
// symlink is specified, then it is also removed.
func Remove(name string) error {
	if !isAdmin() {
		return errNotAdmin
	}
	if id := ole.NewGUID(name); id != nil {
		return (&ShadowCopy{ID: id.String()}).Remove()
	}
	sc, symlink, err := get(name)
	if err != nil {
		return err
	}
	if err = sc.Remove(); err == nil && symlink != "" {
		err = syscall.RemoveDirectory(utf16Ptr(symlink))
	}
	return err
}

// ShadowCopy is an instance of Win32_ShadowCopy class.
type ShadowCopy struct {
	ID           string
	InstallDate  time.Time
	DeviceObject string
	VolumeName   string
}

const (
	scSelect    = "SELECT ID,InstallDate,DeviceObject,VolumeName FROM Win32_ShadowCopy"
	scDevPrefix = `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopy`
)

// unpack converts Win32_ShadowCopy object into ShadowCopy.
func unpack(v *ole.IDispatch) (*ShadowCopy, error) {
	sc := new(ShadowCopy)
	if err := getProp(v, "ID", &sc.ID); err != nil {
		return nil, err
	}
	tryGetProp(v, "DeviceObject", &sc.DeviceObject)
	tryGetProp(v, "InstallDate", &sc.InstallDate)
	tryGetProp(v, "VolumeName", &sc.VolumeName)
	return sc, nil
}

// Get returns the ShadowCopy for the specified ID, DeviceObject, or symlink
// path.
func Get(name string) (*ShadowCopy, error) {
	if !isAdmin() {
		return nil, errNotAdmin
	}
	sc, _, err := get(name)
	return sc, err
}

// get returns the ShadowCopy for the specified ID, DeviceObject, or symlink
// path. If name is a symlink, then it also returns the Cleaned path.
func get(name string) (sc *ShadowCopy, symlink string, err error) {
	var wql string
	if id := ole.NewGUID(name); id != nil {
		wql = fmt.Sprintf(scSelect+" WHERE ID=%q", id.String())
	} else {
		if name = filepath.Clean(name); !hasPrefixFold(name, scDevPrefix) {
			const prefix = `\\?\`
			var buf [syscall.MAX_PATH]byte
			n, err := syscall.Readlink(name, buf[copy(buf[:], prefix):])
			if err != nil {
				return nil, "", fmt.Errorf("vss: not a symlink: %s (%w)", name, err)
			}
			dev := string(buf[:len(prefix)+n])
			if !hasPrefixFold(dev, scDevPrefix) {
				return nil, "", fmt.Errorf("vss: not a shadow copy symlink: %s", name)
			}
			symlink, name = name, dev
		}
		wql = fmt.Sprintf(scSelect+" WHERE DeviceObject=%q", strings.TrimSuffix(name, `\`))
	}
	err = wmiExec(func(s *sWbemServices) (err error) {
		sc, err = queryOne(s, wql, unpack)
		return
	})
	return
}

// List returns existing shadow copies. If vol is non-empty, only shadow copies
// for the specified volume are turned.
func List(vol string) ([]*ShadowCopy, error) {
	if !isAdmin() {
		return nil, errNotAdmin
	}
	var wql = scSelect
	if vol != "" {
		vol, err := volumeName(vol)
		if err != nil {
			return nil, err
		}
		wql = fmt.Sprintf(scSelect+" WHERE VolumeName=%q", vol)
	}
	var all []*ShadowCopy
	err := wmiExec(func(s *sWbemServices) error {
		return s.execQuery(wql, func(v *ole.IDispatch) error {
			sc, err := unpack(v)
			if err == nil {
				all = append(all, sc)
			}
			return err
		})
	})
	return all, err
}

// VolumePath returns the drive letter and/or folder where the shadow copy's
// original volume is mounted. If the volume is mounted at multiple locations,
// only the first one is returned.
func (sc *ShadowCopy) VolumePath() (string, error) {
	m, err := volumePaths(sc.VolumeName)
	if err != nil || len(m) == 0 {
		return "", err
	}
	return m[0], nil
}

// Link creates a directory symlink pointing to the contents of the shadow copy.
func (sc *ShadowCopy) Link(name string) error {
	return syscall.CreateSymbolicLink(utf16Ptr(name), utf16Ptr(sc.DeviceObject+`\`),
		syscall.SYMBOLIC_LINK_FLAG_DIRECTORY)
}

// Remove removes the shadow copy.
func (sc *ShadowCopy) Remove() error {
	if !isAdmin() {
		return errNotAdmin
	}
	return wmiExec(func(s *sWbemServices) error {
		_, err := s.CallMethod("Delete", fmt.Sprintf("Win32_ShadowCopy.ID=%q", sc.ID))
		return err
	})
}

// SplitVolume splits an absolute file path into its volume mount point and the
// path relative to the mount. For example, "C:\Windows\System32" returns "C:\"
// and "Windows\System32".
func SplitVolume(name string) (vol string, rel string, err error) {
	if name = filepath.Clean(name); !filepath.IsAbs(name) {
		// We don't want GetVolumePathName returning the boot volume for
		// relative paths.
		return "", "", fmt.Errorf("vss: path without volume: %s", name)
	}
	var buf [syscall.MAX_PATH]uint16
	if err = windows.GetVolumePathName(utf16Ptr(name), &buf[0], uint32(len(buf))); err != nil {
		return "", "", fmt.Errorf("vss: GetVolumePathName failed for: %s (%w)", name, err)
	}
	vol = syscall.UTF16ToString(buf[:])
	rel, err = filepath.Rel(vol, name)
	return
}

// isAdmin returns whether the current thread is a member of the Administrators
// group.
var isAdmin = sync.OnceValue(func() bool {
	// https://learn.microsoft.com/en-us/windows/win32/api/securitybaseapi/nf-securitybaseapi-checktokenmembership#examples
	var AdministratorsGroup *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&AdministratorsGroup,
	)
	if err != nil {
		return false
	}
	defer func() {
		if err := windows.FreeSid(AdministratorsGroup); err != nil {
			panic(err)
		}
	}()
	ok, err := windows.Token(0).IsMember(AdministratorsGroup)
	return ok && err == nil
})

// createCodeString translates Win32_ShadowCopy.Create return code to a string.
var createCodeString = map[int64]string{
	0:  "Success",
	1:  "Access denied",
	2:  "Invalid argument",
	3:  "Specified volume not found",
	4:  "Specified volume not supported",
	5:  "Unsupported shadow copy context",
	6:  "Insufficient storage",
	7:  "Volume is in use",
	8:  "Maximum number of shadow copies reached",
	9:  "Another shadow copy operation is already in progress",
	10: "Shadow copy provider vetoed the operation",
	11: "Shadow copy provider not registered",
	12: "Shadow copy provider failure",
	13: "Unknown error",
}

// create creates a new shadow copy of the specified volume and returns its ID.
func create(s *sWbemServices, vol string) (*ole.GUID, error) {
	if vol = filepath.FromSlash(vol); vol != "" && vol[len(vol)-1] != '\\' {
		vol += `\` // Trailing separator is required
	}
	sc, err := s.CallMethod("Get", "Win32_ShadowCopy")
	if err != nil {
		return nil, fmt.Errorf("vss: failed to get Win32_ShadowCopy (%w)", err)
	}
	defer mustClear(sc)
	var id string
	rc, err := sc.ToIDispatch().CallMethod("Create", vol, "ClientAccessible", &id)
	if err != nil {
		return nil, fmt.Errorf("vss: Win32_ShadowCopy.Create(%#q) failed (%w)", vol, err)
	}
	if g := ole.NewGUID(id); rc.Val == 0 && g != nil {
		return g, nil
	}
	return nil, fmt.Errorf("vss: Win32_ShadowCopy.Create(%#q) returned %d (%s)",
		vol, rc.Val, createCodeString[rc.Val])
}

// volumeName converts a drive letter or a mounted folder to `\\?\Volume{GUID}\`
// format. If vol is already in the GUID format, it is returned unmodified,
// except for the addition of a trailing slash.
func volumeName(name string) (string, error) {
	const volLen = len(`\\?\Volume{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}\`)
	if name = filepath.FromSlash(name); name != "" && name[len(name)-1] != '\\' {
		name += `\` // Trailing separator is required
	}
	if len(name) != volLen || !hasPrefixFold(name, `\\?\Volume{`) {
		var buf [volLen + 1]uint16
		err := windows.GetVolumeNameForVolumeMountPoint(utf16Ptr(name), &buf[0], uint32(len(buf)))
		if err != nil {
			return "", fmt.Errorf("vss: failed to get volume name of %#q (%w)", name, err)
		}
		name = syscall.UTF16ToString(buf[:])
	}
	return name, nil
}

// volumePaths returns all mount points for the specified volume name.
func volumePaths(vol string) ([]string, error) {
	var buf [2 * syscall.MAX_PATH]uint16
	var n uint32
	err := windows.GetVolumePathNamesForVolumeName(utf16Ptr(vol), &buf[0], uint32(len(buf)), &n)
	if err != nil || len(buf) < int(n) {
		return nil, fmt.Errorf("vss: failed to get volume paths for %#q (%w)", vol, err)
	}
	var all []string
	for b := buf[:n]; len(b) > 1; {
		i := 0
		for i < len(b) && b[i] != 0 {
			i++
		}
		all = append(all, syscall.UTF16ToString(b[:i]))
		b = b[min(i+1, len(b)):]
	}
	return all, nil
}

// hasPrefixFold tests whether s begins with an ASCII-only prefix ignoring case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[0:len(prefix)], prefix)
}

// utf16Ptr converts s to UTF-16 format for Windows API calls. It panics if s
// contains any NUL bytes.
func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic("vss: string with NUL passed to UTF16PtrFromString")
	}
	return p
}
