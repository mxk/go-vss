//go:build windows

package vss

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListGet(t *testing.T) {
	if !isAdmin() {
		t.Skip("not running as admin")
	}
	all, err := List("")
	require.NoError(t, err)
	if len(all) == 0 {
		return
	}
	want := all[0]
	have, err := Get(want.ID)
	require.NoError(t, err)
	require.Equal(t, want, have)
	have, err = Get(want.DeviceObject)
	require.NoError(t, err)
	require.Equal(t, want, have)
}

func TestSplitVol(t *testing.T) {
	_, _, err := SplitVolume(`.`)
	assert.Error(t, err)
	_, _, err = SplitVolume(`C:`)
	assert.Error(t, err)

	vol, rel, err := SplitVolume(`C:\`)
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
