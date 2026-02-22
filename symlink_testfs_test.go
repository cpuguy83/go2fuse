package go2fuse

import (
	iofs "io/fs"
	"path"
	"testing"
	"testing/fstest"
)

// symlinkMapFS wraps an fstest.MapFS to implement readLinkFS.
// On Go 1.25+, fstest.MapFS natively implements io/fs.ReadLinkFS.
// This wrapper exists so tests compile on Go 1.24 without build tags.
// When Go 1.24 support is dropped, delete this file and use MapFS directly.
type symlinkMapFS struct {
	fstest.MapFS
}

func (f *symlinkMapFS) Open(name string) (iofs.File, error) {
	return f.MapFS.Open(name)
}

func (f *symlinkMapFS) ReadLink(name string) (string, error) {
	entry, ok := f.MapFS[name]
	if !ok {
		return "", &iofs.PathError{Op: "readlink", Path: name, Err: iofs.ErrNotExist}
	}
	if entry.Mode&iofs.ModeSymlink == 0 {
		return "", &iofs.PathError{Op: "readlink", Path: name, Err: iofs.ErrInvalid}
	}
	return string(entry.Data), nil
}

func (f *symlinkMapFS) Lstat(name string) (iofs.FileInfo, error) {
	if !iofs.ValidPath(name) {
		return nil, &iofs.PathError{Op: "lstat", Path: name, Err: iofs.ErrInvalid}
	}

	// For symlinks, return info without following.
	if entry, ok := f.MapFS[name]; ok && entry.Mode&iofs.ModeSymlink != 0 {
		return &staticFileInfo{
			name: path.Base(name),
			mode: entry.Mode,
		}, nil
	}

	// For non-symlinks, Stat and Lstat are equivalent.
	return f.MapFS.Stat(name)
}

// newSymlinkFS returns a readLinkFS backed by fstest.MapFS with symlink entries.
// On Go 1.25+, MapFS natively implements readLinkFS and is returned unwrapped.
// On Go 1.24, the symlinkMapFS wrapper provides the interface.
func newSymlinkFS(t *testing.T) readLinkFS {
	t.Helper()
	m := fstest.MapFS{
		"target.txt":  &fstest.MapFile{Data: []byte("hello"), Mode: 0644},
		"link.txt":    &fstest.MapFile{Data: []byte("target.txt"), Mode: iofs.ModeSymlink},
		"regular.txt": &fstest.MapFile{Data: []byte("regular"), Mode: 0644},
	}
	if rl, ok := iofs.FS(m).(readLinkFS); ok {
		return rl
	}
	return &symlinkMapFS{MapFS: m}
}
