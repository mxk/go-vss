package vss

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDateTime(t *testing.T) {
	require.NoError(t, initCOM())
	defer uninitCOM()
	zone := time.FixedZone("EST", -300*60)

	want := time.Date(2023, 12, 13, 01, 22, 50, 108_124_000, zone)
	v, err := parseDateTime("20231213012250.108124-300")
	require.NoError(t, err)
	require.Equal(t, want, v.In(zone))

	want = time.Date(2023, 12, 13, 01, 22, 50, 108_000_000, zone).Local()
	v, err = parseDateTime("20231213012250.108000-300")
	require.NoError(t, err)
	require.Equal(t, want, v)
}

func BenchmarkParseDateTime(b *testing.B) {
	wmiExec(func(s *sWbemServices) error {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = parseDateTime("20231213012250.108124-300")
		}
		b.StopTimer()
		return nil
	})
}

func BenchmarkParseDateTime2(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = parseDateTime2("20231213012250.108124-300")
	}
}
