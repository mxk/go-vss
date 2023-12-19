package vss

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVolName(t *testing.T) {
	name, err := volName(`C:`)
	require.NoError(t, err)
	paths, err := volPaths(name)
	require.NoError(t, err)
	require.Equal(t, []string{`C:\`}, paths)
}
