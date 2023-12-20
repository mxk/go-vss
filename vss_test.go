//go:build windows

package vss

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitVol(t *testing.T) {
	vol, rel, err := SplitVolume(`.`)
	assert.Error(t, err)
	vol, rel, err = SplitVolume(`C:`)
	assert.Error(t, err)

	vol, rel, err = SplitVolume(`C:\`)
	require.NoError(t, err)
	assert.Equal(t, []string{`C:\`, `.`}, []string{vol, rel})

	vol, rel, err = SplitVolume(`C:\Windows\System32`)
	require.NoError(t, err)
	assert.Equal(t, []string{`C:\`, `Windows\System32`}, []string{vol, rel})
}

func TestVolName(t *testing.T) {
	name, err := volumeName(`C:`)
	require.NoError(t, err)
	paths, err := volumePaths(name)
	require.NoError(t, err)
	require.Equal(t, []string{`C:\`}, paths)
}
