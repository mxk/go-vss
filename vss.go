//go:build windows

package vss

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/go-ole/go-ole"
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

// List lists the properties of all shadow copies to stderr.
func List() error {
	return wmiExec(func(s *sWbemServices) error {
		return s.execQuery("SELECT * FROM Win32_ShadowCopy", func(sc *ole.IDispatch) error {
			dumpProps(sc)
			return nil
		})
	})
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
	_, err := s.CallMethod("Delete", fmt.Sprintf(`Win32_ShadowCopy.ID="%s"`, id))
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
	query := fmt.Sprintf(`SELECT DeviceObject FROM Win32_ShadowCopy WHERE ID="%s"`, id)
	return queryOne(s, query, func(sc *ole.IDispatch) (string, error) {
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
	query := fmt.Sprintf(`SELECT ID FROM Win32_ShadowCopy WHERE DeviceObject="%s"`,
		strings.ReplaceAll(dev, `\`, `\\`))
	return queryOne(s, query, func(sc *ole.IDispatch) (*ole.GUID, error) {
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

func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic("vss: string with NUL passed to UTF16PtrFromString")
	}
	return p
}
