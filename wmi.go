//go:build windows

package vss

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

// sWbemServices is an instance of SWbemServices object.
type sWbemServices struct{ ole.IDispatch }

// wmiExec calls fn after initializing the COM library. sWbemServices is
// released automatically when fn returns.
func wmiExec(fn func(s *sWbemServices) error) error {
	uninit, err := initCOM()
	if err != nil {
		return err
	}
	defer uninit()
	s, err := connectServer()
	if err != nil {
		return err
	}
	defer s.Release()
	return fn(s)
}

// initCOM initializes the COM library. If successful, it returns a function
// that must be called to release all COM resources.
func initCOM() (uninit func(), err error) {
	runtime.LockOSThread()
	defer func() {
		if err != nil {
			runtime.UnlockOSThread()
		}
	}()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var e *ole.OleError
		if !errors.As(err, &e) || (e.Code() != ole.S_OK && e.Code() != uintptr(windows.S_FALSE)) {
			return nil, err
		}
	}
	return func() { ole.CoUninitialize(); runtime.UnlockOSThread() }, nil
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
	sWbemLocator := (*ole.IDispatch)(unsafe.Pointer(unk))
	defer sWbemLocator.Release()
	v, err := sWbemLocator.CallMethod("ConnectServer", nil, `root\CIMV2`)
	if err != nil {
		return nil, fmt.Errorf("vss: ConnectServer failed (%w)", err)
	}
	defer mustClear(v) // Calls Release
	s := (*sWbemServices)(unsafe.Pointer(v.ToIDispatch()))
	s.AddRef()
	return s, nil
}

type iterFunc func(*ole.IDispatch) error

// execQuery executes a WQL query and calls fn for each returned object.
func (s *sWbemServices) execQuery(query string, fn iterFunc) error {
	// https://learn.microsoft.com/en-us/windows/win32/api/wbemdisp/ne-wbemdisp-wbemflagenum
	const (
		wbemFlagForwardOnly       = 0x20
		wbemFlagReturnImmediately = 0x10
	)
	// https://learn.microsoft.com/en-us/windows/win32/wmisdk/example--getting-wmi-data-from-the-local-computer
	// https://learn.microsoft.com/en-us/windows/win32/wmisdk/improving-enumeration-performance
	q, err := s.CallMethod("ExecQuery", query, "WQL", wbemFlagForwardOnly|wbemFlagReturnImmediately)
	if err != nil {
		return fmt.Errorf("vss: ExecQuery failed (%w)", err)
	}
	defer mustClear(q)
	return oleutil.ForEach(q.ToIDispatch(), func(v *ole.VARIANT) error {
		defer mustClear(v)
		return fn(v.ToIDispatch())
	})
}

// queryOne executes a query expecting to get exactly one object and returns the
// result of calling get on that object.
func queryOne[T any](s *sWbemServices, query string, get func(v *ole.IDispatch) (T, error)) (T, error) {
	var out T
	var ok bool
	err := s.execQuery(query, func(v *ole.IDispatch) (err error) {
		if ok {
			return fmt.Errorf("vss: multiple matches: %s", query)
		}
		ok = true
		out, err = get(v)
		return
	})
	if err == nil && !ok {
		err = fmt.Errorf("vss: not found: %s", query)
	}
	return out, err
}

// getProps returns all properties of v in a map.
func getProps(v *ole.IDispatch) (map[string]any, error) {
	p, err := v.GetProperty("Properties_")
	if err != nil {
		return nil, err
	}
	defer mustClear(p)
	all := make(map[string]any)
	err = oleutil.ForEach(p.ToIDispatch(), func(v *ole.VARIANT) error {
		defer mustClear(v)
		p := v.ToIDispatch()
		name, err := p.GetProperty("Name")
		if err != nil {
			return err
		}
		defer mustClear(name)
		val, err := p.GetProperty("Value")
		if err != nil {
			return err
		}
		defer mustClear(val)
		if val.VT == ole.VT_UNKNOWN || val.VT == ole.VT_DISPATCH {
			all[name.ToString()] = "<object>" // References will be invalid
		} else {
			all[name.ToString()] = val.Value()
		}
		return nil
	})
	return all, err
}

// dumpProps writes all properties of v to stderr sorted by name.
func dumpProps(v *ole.IDispatch) {
	all, err := getProps(v)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "dumpProps error:", err)
		return
	}
	keys, w := make([]string, 0, len(all)), 0
	for k := range all {
		keys, w = append(keys, k), max(len(k), w)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(os.Stderr, "%*s: %v\n", w, k, all[k])
	}
	_, _ = fmt.Fprintln(os.Stderr)
}

// mustClear panics if VariantClear returns an error.
func mustClear(v *ole.VARIANT) {
	if err := v.Clear(); err != nil {
		panic(err)
	}
}
