package helpers

import (
	"io/fs"
	"os"
)

type mockDirEntry struct {
	name string
	dir  bool
}

func (m mockDirEntry) Name() string {
	return m.name
}

func (m mockDirEntry) IsDir() bool {
	return m.dir
}

func (m mockDirEntry) Type() fs.FileMode {
	if m.dir {
		return fs.ModeDir
	} else {
		return fs.ModeTemporary
	}
}

func (m mockDirEntry) Info() (fs.FileInfo, error) {
	panic("implement me")
}

var _ os.DirEntry = mockDirEntry{}

func MockDirEntry(name string, dir bool) os.DirEntry {
	return mockDirEntry{name, dir}
}
