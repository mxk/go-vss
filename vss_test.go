//go:build windows

package vss

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ExampleCreate() {
	// Create new shadow copy
	id, err := Create("C:")
	if err != nil {
		panic(err)
	}
	defer Remove(id)

	// Get properties
	sc, err := Get(id)
	if err != nil {
		panic(err)
	}

	// Read contents
	dir, err := os.ReadDir(sc.DeviceObject)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Contents of shadow copy %s:\n", sc.ID)
	for _, e := range dir {
		fmt.Println(e.Type(), e.Name())
	}
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

func TestVolName(t *testing.T) {
	_, err := volumeName(``)
	require.Error(t, err)
	name, err := volumeName(`C:`)
	require.NoError(t, err)
	paths, err := volumePaths(name)
	require.NoError(t, err)
	require.Equal(t, []string{`C:\`}, paths)
}
