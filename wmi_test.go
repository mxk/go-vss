package vss

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDateTime(t *testing.T) {
	zone := time.FixedZone("EST", -300*60)
	want := time.Date(2023, 12, 13, 01, 22, 50, 108_124_000, zone)
	v, err := parseDateTime("20231213012250.108124-300")
	require.NoError(t, err)
	require.Equal(t, want, v.In(zone))
	require.Equal(t, want.Local(), v)
}
