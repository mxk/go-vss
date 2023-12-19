//go:build windows

package vss

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// sWbemServices is an instance of SWbemServices object.
type sWbemServices struct{ ole.IDispatch }

// wmiExec calls fn after initializing the COM library. sWbemServices and all
// COM resources are released when fn returns.
func wmiExec(fn func(s *sWbemServices) error) error {
	if err := initCOM(); err != nil {
		return err
	}
	defer uninitCOM()
	s, err := connectServer()
	if err != nil {
		return err
	}
	defer s.Release()
	return fn(s)
}

// initCOM initializes the COM library.
func initCOM() (err error) {
	runtime.LockOSThread()
	defer func() {
		if err != nil {
			runtime.UnlockOSThread()
		}
	}()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		const sFALSE = 1
		var e *ole.OleError
		if !errors.As(err, &e) || (e.Code() != ole.S_OK && e.Code() != sFALSE) {
			return fmt.Errorf("vss: CoInitializeEx failed (%w)", err)
		}
	}
	return nil
}

// uninitCOM releases all COM resources.
func uninitCOM() {
	ole.CoUninitialize()
	runtime.UnlockOSThread()
}

var (
	clsidSWbemLocator = ole.NewGUID("{76A64158-CB41-11D1-8B02-00600806D9B6}")
	iidISWbemLocator  = ole.NewGUID("{76A6415B-CB41-11D1-8B02-00600806D9B6}")
)

// connectServer calls SWbemLocator.ConnectServer and returns an SWbemServices
// object.
func connectServer() (*sWbemServices, error) {
	unk, err := ole.CreateInstance(clsidSWbemLocator, iidISWbemLocator)
	if err != nil {
		return nil, fmt.Errorf("vss: failed to create SWbemLocator (%w)", err)
	}
	defer unk.Release()
	sWbemLocator := (*ole.IDispatch)(unsafe.Pointer(unk))
	vs, err := sWbemLocator.CallMethod("ConnectServer", nil, `root\CIMV2`)
	if err != nil {
		return nil, fmt.Errorf("vss: ConnectServer failed (%w)", err)
	}
	defer mustClear(vs)
	s := (*sWbemServices)(unsafe.Pointer(vs.ToIDispatch()))
	s.AddRef() // Prevent mustClear from freeing the object
	return s, nil
}

// execQuery executes a WQL query and calls fn for each returned object.
func (s *sWbemServices) execQuery(wql string, fn func(*ole.IDispatch) error) error {
	// https://learn.microsoft.com/en-us/windows/win32/api/wbemdisp/ne-wbemdisp-wbemflagenum
	const (
		wbemFlagForwardOnly       = 0x20
		wbemFlagReturnImmediately = 0x10
	)
	// https://learn.microsoft.com/en-us/windows/win32/wmisdk/example--getting-wmi-data-from-the-local-computer
	// https://learn.microsoft.com/en-us/windows/win32/wmisdk/improving-enumeration-performance
	v, err := s.CallMethod("ExecQuery", wql, "WQL", wbemFlagForwardOnly|wbemFlagReturnImmediately)
	if err != nil {
		return fmt.Errorf("vss: ExecQuery failed (%w)", err)
	}
	defer mustClear(v)
	return oleutil.ForEach(v.ToIDispatch(), func(v *ole.VARIANT) error {
		defer mustClear(v)
		return fn(v.ToIDispatch())
	})
}

// queryOne executes a query expecting to get exactly one object and returns the
// result of calling fn on it.
func queryOne[T any](s *sWbemServices, wql string, fn func(v *ole.IDispatch) (T, error)) (T, error) {
	var out T
	var ok bool
	err := s.execQuery(wql, func(v *ole.IDispatch) (err error) {
		if ok {
			return fmt.Errorf("vss: multiple matches: %s", wql)
		}
		ok = true
		out, err = fn(v)
		return
	})
	if err == nil && !ok {
		err = fmt.Errorf("vss: not found: %s", wql)
	}
	return out, err
}

// getProps returns all properties of v in a map.
func getProps(v *ole.IDispatch) (map[string]any, error) {
	vps, err := v.GetProperty("Properties_")
	if err != nil {
		return nil, fmt.Errorf("vss: failed to get Properties_ (%w)", err)
	}
	defer mustClear(vps)
	all := make(map[string]any)
	err = oleutil.ForEach(vps.ToIDispatch(), func(v *ole.VARIANT) error {
		defer mustClear(v)
		p := v.ToIDispatch()
		vname, err := p.GetProperty("Name")
		if err != nil {
			return fmt.Errorf("vss: failed to get Name property (%w)", err)
		}
		defer mustClear(vname)
		vval, err := p.GetProperty("Value")
		if err != nil {
			return fmt.Errorf("vss: failed to get Value property (%w)", err)
		}
		defer mustClear(vval)
		switch name := vname.ToString(); vval.VT {
		case ole.VT_UNKNOWN, ole.VT_DISPATCH:
			all[name] = vval.VT.String() // References will be invalid
		default:
			all[name] = vval.Value()
		}
		return nil
	})
	return all, err
}

// dumpProps writes all properties of v to stderr sorted by name.
func dumpProps(v *ole.IDispatch) {
	var b bytes.Buffer
	defer func() { _, _ = fmt.Fprintf(os.Stderr, "%s\n", b.Bytes()) }()
	all, err := getProps(v)
	if err != nil {
		_, _ = fmt.Fprintln(&b, "dumpProps error:", err)
		return
	}
	keys, w := make([]string, 0, len(all)), 0
	for k := range all {
		keys, w = append(keys, k), max(len(k), w)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(&b, "%*s: %v\n", w, k, all[k])
	}
}

// parseDateTime converts a WMI datetime string (yyyymmddHHMMSS.mmmmmmsUUU) to
// time.Time.
func parseDateTime(dt string) (time.Time, error) {
	// This logic is the same as creating an SWbemDateTime object, setting its
	// Value property, and calling GetFileTime method, but much faster.
	const sign = 21
	if len(dt) != sign+4 || (dt[sign] != '-' && dt[sign] != '+') {
		return time.Time{}, fmt.Errorf("vss: invalid datetime: %s", dt)
	}
	// https://learn.microsoft.com/en-us/windows/win32/wmisdk/swbemdatetime-utc
	off, err := strconv.Atoi(dt[sign:])
	if err != nil || off < -720 || 720 < off {
		return time.Time{}, fmt.Errorf("vss: invalid UTC offset: %s", dt)
	}
	// https://learn.microsoft.com/en-us/windows/win32/wmisdk/cim-datetime
	tz := time.FixedZone("", off*60)
	t, err := time.ParseInLocation("20060102150405.000000", dt[:sign], tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("vss: failed to parse datetime: %s", dt)
	}
	return t.Local(), nil
}

// mustClear panics if VariantClear returns an error. If v is a VT_UNKNOWN or
// VT_DISPATCH, then this also releases the object.
func mustClear(v *ole.VARIANT) {
	if err := v.Clear(); err != nil {
		panic(err)
	}
}
