//go:build windows

package vss

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

// Create creates a new shadow copy of the specified volume (e.g. "C:") and
// returns its ID.
func Create(vol string) (string, error) {
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

// Link creates a directory symbolic link pointing to the contents of the
// specified shadow copy ID.
func Link(link, id string) error {
	g, err := parseID(id)
	if err != nil {
		return err
	}
	var dev string
	err = wmiExec(func(s *sWbemServices) (err error) {
		dev, err = deviceObjectOfID(s, g)
		return
	})
	if err != nil {
		return err
	}
	return syscall.CreateSymbolicLink(utf16Ptr(link), utf16Ptr(dev+`\`),
		syscall.SYMBOLIC_LINK_FLAG_DIRECTORY)
}

// LinkNew creates a new shadow copy and links it at the specified path.
// The shadow copy is deleted if the operation fails.
func LinkNew(link, vol string) (err error) {
	id, err := Create(vol)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = Delete(id)
		}
	}()
	return Link(link, id)
}

// Delete deletes a shadow copy, which can be specified either by ID or a file
// system path to a symlink where the shadow copy is mounted.
func Delete(idOrLink string) error {
	if g, err := parseID(idOrLink); err == nil {
		return wmiExec(func(s *sWbemServices) error { return deleteByID(s, g) })
	}
	var buf [syscall.MAX_PATH]byte
	const prefix = `\\?\`
	n, err := syscall.Readlink(idOrLink, buf[copy(buf[:], prefix):])
	if err != nil {
		return fmt.Errorf("vss: not a valid symlink: %s (%w)", idOrLink, err)
	}
	dev := strings.TrimSuffix(string(buf[:len(prefix)+n]), `\`)
	if len(dev) == len(buf) || !strings.HasPrefix(dev, `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopy`) {
		return fmt.Errorf("vss: not a valid shadow copy symlink: %s", idOrLink)
	}
	err = wmiExec(func(s *sWbemServices) error { return deleteByDeviceObject(s, dev) })
	if err != nil {
		return err
	}
	return syscall.RemoveDirectory(utf16Ptr(idOrLink))
}

// ShadowCopy is a subset of Win32_ShadowCopy properties.
type ShadowCopy struct {
	DeviceObject string
	ID           string
	InstallDate  time.Time
	VolumeName   string
}

// List returns information about existing shadow copies. If vol is non-empty,
// only shadow copies for the specified volume are turned.
func List(vol string) ([]*ShadowCopy, error) {
	var wql = "SELECT DeviceObject,ID,InstallDate,VolumeName FROM Win32_ShadowCopy"
	if vol != "" {
		vol, err := volumeName(vol)
		if err != nil {
			return nil, err
		}
		wql += fmt.Sprintf(" WHERE VolumeName=%q", vol)
	}
	var all []*ShadowCopy
	err := wmiExec(func(s *sWbemServices) error {
		return s.execQuery(wql, func(v *ole.IDispatch) (err error) {
			m, err := getProps(v)
			if err != nil {
				return err
			}
			t, err := parseDateTime(m["InstallDate"].(string))
			if err != nil {
				return err
			}
			all = append(all, &ShadowCopy{
				DeviceObject: m["DeviceObject"].(string),
				ID:           m["ID"].(string),
				InstallDate:  t,
				VolumeName:   m["VolumeName"].(string),
			})
			return nil
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
	sort.Strings(m)
	return m[0], nil
}

// SplitVolume splits an absolute file path into its volume mount point and the
// path relative to the mount. For example, "C:\Windows" returns "C:\" and
// "Windows".
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

// create creates a new shadow copy of the specified volume and
// returns its ID.
func create(s *sWbemServices, vol string) (*ole.GUID, error) {
	// TODO: Directory mounts
	vol = filepath.VolumeName(vol) + `\` // Trailing separator is required
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

// deleteByID deletes a shadow copy by ID.
func deleteByID(s *sWbemServices, id *ole.GUID) error {
	_, err := s.CallMethod("Delete", fmt.Sprintf("Win32_ShadowCopy.ID=%q", id))
	return err
}

// deleteByDeviceObject deletes a shadow copy by DeviceObject path.
func deleteByDeviceObject(s *sWbemServices, dev string) error {
	id, err := idOfDeviceObject(s, dev)
	if err != nil {
		return err
	}
	return deleteByID(s, id)
}

// deviceObjectOfID returns the DeviceObject property of the specified shadow
// copy ID.
func deviceObjectOfID(s *sWbemServices, id *ole.GUID) (string, error) {
	wql := fmt.Sprintf("SELECT DeviceObject FROM Win32_ShadowCopy WHERE ID=%q", id)
	return queryOne(s, wql, func(sc *ole.IDispatch) (string, error) {
		if p, err := sc.GetProperty("DeviceObject"); err == nil {
			defer mustClear(p)
			return p.ToString(), nil
		} else {
			return "", err
		}
	})
}

// idOfDeviceObject returns the ID property of the specified shadow copy
// DeviceObject.
func idOfDeviceObject(s *sWbemServices, dev string) (*ole.GUID, error) {
	wql := fmt.Sprintf("SELECT ID FROM Win32_ShadowCopy WHERE DeviceObject=%q", dev)
	return queryOne(s, wql, func(sc *ole.IDispatch) (*ole.GUID, error) {
		if p, err := sc.GetProperty("ID"); err == nil {
			defer mustClear(p)
			return parseID(p.ToString())
		} else {
			return nil, err
		}
	})
}

// parseID ensures that id is a valid GUID.
func parseID(id string) (*ole.GUID, error) {
	if g := ole.NewGUID(id); g != nil {
		return g, nil
	}
	return nil, fmt.Errorf("vss: invalid ID %q", id)
}

// volumeName converts a drive letter or a mounted folder to `\\?\Volume{GUID}\`
// format. If vol is already in the GUID format, it is returned unmodified.
func volumeName(vol string) (string, error) {
	const volLen = len(`\\?\Volume{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}\`)
	if vol = filepath.FromSlash(vol); vol[len(vol)-1] != '\\' {
		vol += `\`
	}
	if len(vol) != volLen || !strings.HasPrefix(vol, `\\?\Volume{`) {
		var buf [volLen + 1]uint16
		err := windows.GetVolumeNameForVolumeMountPoint(utf16Ptr(vol), &buf[0], uint32(len(buf)))
		if err != nil {
			return "", fmt.Errorf("vss: failed to get volume name of %#q (%w)", vol, err)
		}
		vol = syscall.UTF16ToString(buf[:])
	}
	return vol, nil
}

// volumePaths returns all mount points for the specified volume name.
func volumePaths(vol string) ([]string, error) {
	var buf [2 * syscall.MAX_PATH]uint16
	var n uint32
	err := windows.GetVolumePathNamesForVolumeName(utf16Ptr(vol), &buf[0], uint32(len(buf)), &n)
	if n--; err != nil || len(buf) < int(n) {
		return nil, fmt.Errorf("vss: failed to get volume paths for %#q (%w)", vol, err)
	}
	var all []string
	for b := buf[:n]; len(b) > 0; {
		i := 0
		for i < len(b) && b[i] != 0 {
			i++
		}
		all = append(all, syscall.UTF16ToString(b[:i]))
		b = b[min(i+1, len(b)):]
	}
	return all, nil
}

// utf16Ptr converts s to UTF-16 format for Windows API calls.
func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic("vss: string with NUL passed to UTF16PtrFromString")
	}
	return p
}
