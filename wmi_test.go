//go:build windows

package vss

import (
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/stretchr/testify/require"
)

func TestGetProp(t *testing.T) {
	var (
		clsidSWbemDateTime = ole.NewGUID("{47DFBE54-CF76-11d3-B38F-00105A1F473A}")
		iidISWbemDateTime  = ole.NewGUID("{5E97458A-CF77-11D3-B38F-00105A1F473A}")
	)
	zone := time.FixedZone("", -300*60)
	want := time.Date(2023, 12, 13, 01, 22, 50, 108_124_000, zone)
	ft := syscall.NsecToFiletime(want.UnixNano())
	fts := strconv.FormatUint(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime), 10)
	err := wmiExec(func(s *sWbemServices) error {
		unk, err := ole.CreateInstance(clsidSWbemDateTime, iidISWbemDateTime)
		require.NoError(t, err)
		defer unk.Release()
		sWbemDateTime := (*ole.IDispatch)(unsafe.Pointer(unk))

		var dflt string
		require.NoError(t, getProp(sWbemDateTime, "Value", &dflt))
		require.Equal(t, "00000101000000.000000+000", dflt)

		_, err = sWbemDateTime.CallMethod("SetFileTime", fts, false)
		require.NoError(t, err)

		var have time.Time
		require.True(t, tryGetProp(sWbemDateTime, "Value", &have))
		require.Equal(t, want, have.In(zone))
		require.False(t, tryGetProp(sWbemDateTime, "Value1", &have))
		return nil
	})
	require.NoError(t, err)
}

func TestGetProps(t *testing.T) {
	want, err := os.Hostname()
	require.NoError(t, err)
	err = wmiExec(func(s *sWbemServices) error {
		const wql = "SELECT DNSHostName FROM Win32_ComputerSystem"
		props, err := queryOne(s, wql, getProps)
		require.NoError(t, err)
		delete(props, "Name")
		require.Equal(t, map[string]any{"DNSHostName": want}, props)
		return nil
	})
	require.NoError(t, err)
}

func TestParseDateTime(t *testing.T) {
	zone := time.FixedZone("", -300*60)
	want := time.Date(2023, 12, 13, 01, 22, 50, 108_124_000, zone)
	v, err := parseDateTime("20231213012250.108124-300")
	require.NoError(t, err)
	require.Equal(t, want, v.In(zone))
	require.Equal(t, want.Local(), v)
}

func TestParseDateTimeLINT(t *testing.T) {
	zone := time.FixedZone("", 14*60*60)
	want := time.Date(2023, 12, 13, 01, 22, 50, 108_124_000, zone)
	v, err := parseDateTime("20231213012250.108124+840")
	require.NoError(t, err)
	require.Equal(t, want, v.In(zone))
	require.Equal(t, want.Local(), v)
}
