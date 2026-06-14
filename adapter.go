// Package go2fuse provides an adapter from Go's io/fs.FS to a FUSE filesystem
// using the hanwen/go-fuse library. The resulting FUSE filesystem is read-only.
package go2fuse

import (
	"context"
	"io"
	iofs "io/fs"
	"path"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// readLinkFS matches io/fs.ReadLinkFS (added in Go 1.25).
// Defined locally to avoid requiring Go 1.25 as the minimum module version.
type readLinkFS interface {
	iofs.FS
	ReadLink(name string) (string, error)
	Lstat(name string) (iofs.FileInfo, error)
}

// lstat returns FileInfo for name without following symlinks when fsys
// implements readLinkFS. Otherwise it falls back to iofs.Stat.
func lstat(fsys iofs.FS, name string) (iofs.FileInfo, error) {
	if rlfs, ok := fsys.(readLinkFS); ok {
		return rlfs.Lstat(name)
	}
	return iofs.Stat(fsys, name)
}

// readLink returns the destination of the named symbolic link.
func readLink(fsys iofs.FS, name string) (string, error) {
	rlfs, ok := fsys.(readLinkFS)
	if !ok {
		return "", &iofs.PathError{Op: "readlink", Path: name, Err: iofs.ErrInvalid}
	}
	return rlfs.ReadLink(name)
}

// New creates a FUSE root node that presents the given io/fs.FS as a read-only
// FUSE filesystem. The returned InodeEmbedder can be passed to fs.Mount or
// fs.NewNodeFS.
func New(fsys iofs.FS) fs.InodeEmbedder {
	return &dirNode{
		fsys: fsys,
		path: ".",
	}
}

// Mount is a convenience function that mounts the given io/fs.FS at mountpoint
// and returns the *fuse.Server. Call server.Wait() to block until unmount.
// If opts is nil, sensible defaults are used.
func Mount(mountpoint string, fsys iofs.FS, opts *fs.Options) (*fuse.Server, error) {
	root := New(fsys)
	return fs.Mount(mountpoint, root, opts)
}

// dirNode represents a directory in the adapted filesystem.
type dirNode struct {
	fs.Inode
	fsys iofs.FS
	path string // path within the fs.FS (e.g. "." for root, "subdir/nested")
}

// Compile-time interface checks.
var _ fs.InodeEmbedder = (*dirNode)(nil)
var _ fs.NodeLookuper = (*dirNode)(nil)
var _ fs.NodeGetattrer = (*dirNode)(nil)
var _ fs.NodeReaddirer = (*dirNode)(nil)

func (d *dirNode) childPath(name string) string {
	if d.path == "." {
		return name
	}
	return path.Join(d.path, name)
}

func (d *dirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	cp := d.childPath(name)

	info, err := lstat(d.fsys, cp)
	if err != nil {
		return nil, toErrno(err)
	}

	node, stable := d.newChild(cp, info)
	fillAttrFromFileInfo(&out.Attr, info)
	child := d.NewInode(ctx, node, stable)
	return child, 0
}

func (d *dirNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	info, err := iofs.Stat(d.fsys, d.path)
	if err != nil {
		return toErrno(err)
	}
	fillAttrFromFileInfo(&out.Attr, info)
	return 0
}

func (d *dirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries, err := iofs.ReadDir(d.fsys, d.path)
	if err != nil {
		return nil, toErrno(err)
	}

	fuseEntries := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		fuseEntries = append(fuseEntries, fuse.DirEntry{
			Name: e.Name(),
			Mode: goModeToFuseType(e.Type()),
		})
	}

	return fs.NewListDirStream(fuseEntries), 0
}

func (d *dirNode) newChild(childPath string, info iofs.FileInfo) (fs.InodeEmbedder, fs.StableAttr) {
	if info.Mode()&iofs.ModeSymlink != 0 {
		return &symlinkNode{
			fsys: d.fsys,
			path: childPath,
		}, fs.StableAttr{Mode: syscall.S_IFLNK}
	}

	if info.IsDir() {
		return &dirNode{
			fsys: d.fsys,
			path: childPath,
		}, fs.StableAttr{Mode: syscall.S_IFDIR}
	}

	return &fileNode{
		fsys: d.fsys,
		path: childPath,
	}, fs.StableAttr{Mode: syscall.S_IFREG}
}

// fileNode represents a regular file in the adapted filesystem.
type fileNode struct {
	fs.Inode
	fsys iofs.FS
	path string
}

// Compile-time interface checks.
var _ fs.InodeEmbedder = (*fileNode)(nil)
var _ fs.NodeGetattrer = (*fileNode)(nil)
var _ fs.NodeOpener = (*fileNode)(nil)
var _ fs.NodeReader = (*fileNode)(nil)

func (f *fileNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	info, err := iofs.Stat(f.fsys, f.path)
	if err != nil {
		return toErrno(err)
	}
	fillAttrFromFileInfo(&out.Attr, info)
	return 0
}

func (f *fileNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Reject writes.
	if flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, 0, syscall.EROFS
	}

	file, err := f.fsys.Open(f.path)
	if err != nil {
		return nil, 0, toErrno(err)
	}

	return &fileHandle{file: file}, fuse.FOPEN_KEEP_CACHE, 0
}

// Read implements fs.NodeReader to handle reads that arrive without a prior
// Open call, which can happen when the kernel negotiates CAP_NO_OPEN_SUPPORT.
// When a file handle is present (normal Open → Read path) it is used directly;
// otherwise the file is opened inline and Read is called on a temporary handle.
func (f *fileNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if fh != nil {
		return fh.(*fileHandle).Read(ctx, dest, off)
	}
	file, err := f.fsys.Open(f.path)
	if err != nil {
		return nil, toErrno(err)
	}
	defer file.Close()
	return (&fileHandle{file: file}).Read(ctx, dest, off)
}

// symlinkNode represents a symbolic link in the adapted filesystem.
type symlinkNode struct {
	fs.Inode
	fsys iofs.FS
	path string
}

// Compile-time interface checks.
var _ fs.InodeEmbedder = (*symlinkNode)(nil)
var _ fs.NodeGetattrer = (*symlinkNode)(nil)
var _ fs.NodeReadlinker = (*symlinkNode)(nil)

func (s *symlinkNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	info, err := lstat(s.fsys, s.path)
	if err != nil {
		return toErrno(err)
	}
	fillAttrFromFileInfo(&out.Attr, info)
	return 0
}

func (s *symlinkNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := readLink(s.fsys, s.path)
	if err != nil {
		return nil, toErrno(err)
	}
	return []byte(target), 0
}

// fileHandle wraps an open fs.File for FUSE read operations.
type fileHandle struct {
	mu   sync.Mutex
	file iofs.File
	pos  int64 // current read position for sequential access (no Seeker)
}

// Compile-time interface checks.
var _ fs.FileReader = (*fileHandle)(nil)
var _ fs.FileReleaser = (*fileHandle)(nil)

func (fh *fileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Use ReaderAt for correct offset-based reads when available.
	// ReaderAt is defined as safe for concurrent use.
	if ra, ok := fh.file.(io.ReaderAt); ok {
		n, err := ra.ReadAt(dest, off)
		if err != nil && err != io.EOF {
			return nil, toErrno(err)
		}
		return fuse.ReadResultData(dest[:n]), 0
	}

	// Both remaining paths require serialization: the seeker path needs
	// seek+read atomicity, and the sequential path needs pos consistency.
	fh.mu.Lock()
	defer fh.mu.Unlock()

	// Seek to the requested offset and read. This handles arbitrary
	// offsets (including concurrent FUSE async reads) correctly.
	if s, ok := fh.file.(io.Seeker); ok {
		if _, err := s.Seek(off, io.SeekStart); err != nil {
			return nil, toErrno(err)
		}
		n, err := fh.file.Read(dest)
		if err != nil && err != io.EOF {
			return nil, toErrno(err)
		}
		return fuse.ReadResultData(dest[:n]), 0
	}

	// For sequential readers, use plain Read when the requested offset
	// matches the current position. This is the common case — FUSE reads
	// arrive sequentially on a file handle.
	if off != fh.pos {
		return nil, syscall.EIO
	}

	n, err := fh.file.Read(dest)
	fh.pos += int64(n)
	if err != nil && err != io.EOF {
		return nil, toErrno(err)
	}
	return fuse.ReadResultData(dest[:n]), 0
}

func (fh *fileHandle) Release(ctx context.Context) syscall.Errno {
	if err := fh.file.Close(); err != nil {
		return toErrno(err)
	}
	return 0
}

// fillAttrFromFileInfo populates a fuse.Attr from an fs.FileInfo.
func fillAttrFromFileInfo(attr *fuse.Attr, info iofs.FileInfo) {
	attr.Size = uint64(info.Size())
	attr.Mode = goModeToFuseMode(info.Mode())

	mtime := info.ModTime()
	if !mtime.IsZero() {
		sec := uint64(mtime.Unix())
		nsec := uint32(mtime.Nanosecond())
		attr.Mtime = sec
		attr.Mtimensec = nsec
		attr.Atime = sec
		attr.Atimensec = nsec
		attr.Ctime = sec
		attr.Ctimensec = nsec
	}
}

// goModeToFuseMode converts Go's fs.FileMode to a POSIX mode (type + permission bits).
func goModeToFuseMode(m iofs.FileMode) uint32 {
	perm := uint32(m.Perm())
	switch {
	case m.IsDir():
		return syscall.S_IFDIR | perm
	case m&iofs.ModeSymlink != 0:
		return syscall.S_IFLNK | perm
	case m&iofs.ModeNamedPipe != 0:
		return syscall.S_IFIFO | perm
	case m&iofs.ModeSocket != 0:
		return syscall.S_IFSOCK | perm
	case m&iofs.ModeDevice != 0:
		if m&iofs.ModeCharDevice != 0 {
			return syscall.S_IFCHR | perm
		}
		return syscall.S_IFBLK | perm
	default:
		return syscall.S_IFREG | perm
	}
}

// goModeToFuseType converts Go's fs.FileMode to the FUSE file type bits only
// (for use in DirEntry.Mode).
func goModeToFuseType(m iofs.FileMode) uint32 {
	switch {
	case m.IsDir():
		return syscall.S_IFDIR
	case m&iofs.ModeSymlink != 0:
		return syscall.S_IFLNK
	default:
		return syscall.S_IFREG
	}
}

// toErrno converts a Go error to a syscall.Errno.
func toErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	return fs.ToErrno(err)
}
