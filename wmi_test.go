//go:build windows

package vss

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWMI(t *testing.T) {
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
	zone := time.FixedZone("EST", -300*60)
	want := time.Date(2023, 12, 13, 01, 22, 50, 108_124_000, zone)
	v, err := parseDateTime("20231213012250.108124-300")
	require.NoError(t, err)
	require.Equal(t, want, v.In(zone))
	require.Equal(t, want.Local(), v)
}
