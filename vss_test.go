//go:build windows

package vss

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Shadow copies returned by vssadmin.exe.
var vssadminList []*ShadowCopy

func TestMain(m *testing.M) {
	vssadmin := filepath.Join(os.Getenv("SystemRoot"), "System32", "vssadmin.exe")
	out, err := exec.Command(vssadmin, "list", "shadows").Output()
	if err != nil {
		if isAdmin() {
			panic(err)
		}
	}
	s := bufio.NewScanner(bytes.NewReader(out))
	var sc *ShadowCopy
	for s.Scan() {
		ln := strings.TrimSpace(s.Text())
		if _, ts, ok := strings.Cut(ln, " shadow copies at creation time: "); ok {
			t, err := time.ParseInLocation("2006-01-02 03:04:05 PM", ts, time.Local)
			if err != nil {
				panic(err)
			}
			if sc != nil {
				vssadminList = append(vssadminList, sc)
			}
			sc = &ShadowCopy{InstallDate: t}
		} else if id, ok := strings.CutPrefix(ln, "Shadow Copy ID: "); ok {
			sc.ID = strings.ToUpper(id)
		} else if vol, ok := strings.CutPrefix(ln, "Original Volume: "); ok {
			i := strings.Index(vol, `\\?\Volume{`)
			sc.VolumeName = vol[i:]
		} else if dev, ok := strings.CutPrefix(ln, "Shadow Copy Volume: "); ok {
			sc.DeviceObject = dev
		}
	}
	if err := s.Err(); err != nil {
		panic(err)
	}
	if sc != nil {
		vssadminList = append(vssadminList, sc)
	}
	os.Exit(m.Run())
}

func TestIsShadowCopy(t *testing.T) {
	if len(vssadminList) == 0 {
		t.Skip("no existing shadow copies")
	}
	sc := vssadminList[0]
	tmp, err := os.MkdirTemp("", "go-vss.")
	require.NoError(t, err)
	defer os.Remove(tmp)

	link := filepath.Join(tmp, "link")
	require.NoError(t, sc.Link(link))
	defer func() {
		if _, err := os.Lstat(link); err == nil {
			_ = rmdir(link)
		}
	}()

	if ok, err := IsShadowCopy(tmp); assert.NoError(t, err) {
		assert.False(t, ok)
	}
	if ok, err := IsShadowCopy(sc.DeviceObject); assert.NoError(t, err) {
		assert.True(t, ok)
	}
	if ok, err := IsShadowCopy(link); assert.NoError(t, err) {
		assert.True(t, ok)
	}
	if have, err := Get(link); assert.NoError(t, err) {
		if have.InstallDate.Sub(sc.InstallDate).Abs() < time.Second {
			have.InstallDate = sc.InstallDate
		}
		assert.Equal(t, sc, have)
	}
	if all, err := os.ReadDir(link); assert.NoError(t, err) && len(all) > 0 {
		file := filepath.Join(link, all[0].Name())
		if ok, err := IsShadowCopy(file); assert.NoError(t, err) {
			assert.True(t, ok)
		}
		file = filepath.Join(sc.DeviceObject, all[0].Name())
		if ok, err := IsShadowCopy(file); assert.NoError(t, err) {
			assert.True(t, ok)
		}
	}
	assert.NoError(t, rmdir(link))
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

	// vssadmin truncates milliseconds
	for _, sc := range all {
		for _, ref := range vssadminList {
			if sc.ID == ref.ID && sc.InstallDate.Sub(ref.InstallDate).Abs() < time.Second {
				ref.InstallDate = sc.InstallDate
				break
			}
		}
	}
	assert.Equal(t, vssadminList, all)

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

func TestShadowPath(t *testing.T) {
	const want = `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopy42`
	assert.False(t, isShadowPath(``))
	assert.False(t, isShadowPath(`C:\Windows`))
	assert.True(t, isShadowPath(`\Device\HarddiskVolumeShadowCopy42`))
	assert.Equal(t, want, normShadowPath(`\device\harddiskvolumeshadowcopy42`))
	assert.Equal(t, want, normShadowPath(`globalroot\device\harddiskvolumeshadowcopy42`))
	assert.Equal(t, want, normShadowPath(want))
}
