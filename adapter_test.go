package go2fuse_test

import (
	"bytes"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cpuguy83/go2fuse"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func mountFS(t testing.TB, fsys iofs.FS) string {
	t.Helper()

	mountpoint := t.TempDir()

	sec := time.Second
	server, err := go2fuse.Mount(mountpoint, fsys, &fs.Options{
		MountOptions: fuse.MountOptions{
			Debug: testing.Verbose(),
		},
		EntryTimeout: &sec,
		AttrTimeout:  &sec,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := server.Unmount(); err != nil {
			t.Log("unmount error:", err)
		}
	})

	return mountpoint
}

func mount(t testing.TB, fsys *fstest.MapFS) string {
	t.Helper()
	return mountFS(t, fsys)
}

func TestReadFile(t *testing.T) {
	content := []byte("hello, fuse world!")

	fsys := &fstest.MapFS{
		"hello.txt": &fstest.MapFile{
			Data:    content,
			Mode:    0644,
			ModTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	mountpoint := mount(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestReadDir(t *testing.T) {
	fsys := &fstest.MapFS{
		"a.txt":        &fstest.MapFile{Data: []byte("a"), Mode: 0644},
		"b.txt":        &fstest.MapFile{Data: []byte("b"), Mode: 0644},
		"subdir/c.txt": &fstest.MapFile{Data: []byte("c"), Mode: 0644},
	}

	mountpoint := mount(t, fsys)

	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}

	for _, want := range []string{"a.txt", "b.txt", "subdir"} {
		if !names[want] {
			t.Errorf("missing entry %q in root directory listing", want)
		}
	}
}

func TestNestedDir(t *testing.T) {
	content := []byte("nested content")
	fsys := &fstest.MapFS{
		"a/b/c/file.txt": &fstest.MapFile{Data: content, Mode: 0644},
	}

	mountpoint := mount(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "a", "b", "c", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestStatFile(t *testing.T) {
	modTime := time.Date(2025, 6, 15, 12, 30, 0, 0, time.UTC)
	data := []byte("stat me")
	fsys := &fstest.MapFS{
		"stat.txt": &fstest.MapFile{
			Data:    data,
			Mode:    0644,
			ModTime: modTime,
		},
	}

	mountpoint := mount(t, fsys)

	info, err := os.Stat(filepath.Join(mountpoint, "stat.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if info.IsDir() {
		t.Error("expected file, got directory")
	}
	if info.Size() != int64(len(data)) {
		t.Errorf("size mismatch: got %d, want %d", info.Size(), len(data))
	}
	if !info.ModTime().Equal(modTime) {
		t.Errorf("modtime mismatch: got %v, want %v", info.ModTime(), modTime)
	}
}

func TestStatDir(t *testing.T) {
	fsys := &fstest.MapFS{
		"mydir/file.txt": &fstest.MapFile{Data: []byte("x"), Mode: 0644},
	}

	mountpoint := mount(t, fsys)

	info, err := os.Stat(filepath.Join(mountpoint, "mydir"))
	if err != nil {
		t.Fatal(err)
	}

	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

func TestNonexistentFile(t *testing.T) {
	fsys := &fstest.MapFS{}

	mountpoint := mount(t, fsys)

	_, err := os.Stat(filepath.Join(mountpoint, "nope.txt"))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected not-exist error, got: %v", err)
	}
}

func TestEmptyFile(t *testing.T) {
	fsys := &fstest.MapFS{
		"empty.txt": &fstest.MapFile{Data: []byte{}, Mode: 0644},
	}

	mountpoint := mount(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

func TestLargeFile(t *testing.T) {
	// 1MB file to test reading beyond a single FUSE buffer.
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 256)
	}

	fsys := &fstest.MapFS{
		"large.bin": &fstest.MapFile{Data: data, Mode: 0644},
	}

	mountpoint := mount(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("large file content mismatch: got %d bytes, want %d bytes", len(got), len(data))
	}
}

func TestReadSubdir(t *testing.T) {
	fsys := &fstest.MapFS{
		"dir/a.txt": &fstest.MapFile{Data: []byte("a"), Mode: 0644},
		"dir/b.txt": &fstest.MapFile{Data: []byte("b"), Mode: 0644},
	}

	mountpoint := mount(t, fsys)

	entries, err := os.ReadDir(filepath.Join(mountpoint, "dir"))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}
	if !names["a.txt"] || !names["b.txt"] {
		t.Errorf("unexpected entries: %v", names)
	}
}

func TestOsDirFS(t *testing.T) {
	// Test with os.DirFS wrapping a real directory.
	dir := t.TempDir()
	content := []byte("real fs content")
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}

	mountpoint := t.TempDir()
	sec := time.Second
	server, err := go2fuse.Mount(mountpoint, os.DirFS(dir), &fs.Options{
		MountOptions: fuse.MountOptions{
			Debug: testing.Verbose(),
		},
		EntryTimeout: &sec,
		AttrTimeout:  &sec,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.Unmount()
	})

	// Read root file.
	got, err := os.ReadFile(filepath.Join(mountpoint, "real.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}

	// Read nested file.
	got, err = os.ReadFile(filepath.Join(mountpoint, "sub", "nested.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "nested" {
		t.Errorf("nested content mismatch: got %q, want %q", got, "nested")
	}

	// List root.
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 root entries, got %d", len(entries))
	}
}

func TestWriteRejected(t *testing.T) {
	fsys := &fstest.MapFS{
		"readonly.txt": &fstest.MapFile{Data: []byte("ro"), Mode: 0644},
	}

	mountpoint := mount(t, fsys)

	err := os.WriteFile(filepath.Join(mountpoint, "readonly.txt"), []byte("new"), 0644)
	if err == nil {
		t.Fatal("expected error writing to read-only filesystem")
	}
}

// readerOnlyFS is an fs.FS whose files only implement io.Reader (no ReaderAt, no Seeker).
// This exercises the sequential read path in fileHandle.Read.
type readerOnlyFS struct {
	files map[string]*readerOnlyFileData
}

type readerOnlyFileData struct {
	data []byte
	mode iofs.FileMode
}

func (f *readerOnlyFS) Open(name string) (iofs.File, error) {
	if name == "." {
		return &readerOnlyDir{fs: f}, nil
	}
	fd, ok := f.files[name]
	if !ok {
		return nil, &iofs.PathError{Op: "open", Path: name, Err: iofs.ErrNotExist}
	}
	return &readerOnlyFile{data: fd.data, mode: fd.mode, name: name}, nil
}

type readerOnlyDir struct {
	fs      *readerOnlyFS
	entries []iofs.DirEntry
	pos     int
}

func (d *readerOnlyDir) Stat() (iofs.FileInfo, error) {
	return &staticFileInfo{name: ".", size: 0, mode: iofs.ModeDir | 0555}, nil
}

func (d *readerOnlyDir) Read([]byte) (int, error) { return 0, io.EOF }
func (d *readerOnlyDir) Close() error             { return nil }

func (d *readerOnlyDir) ReadDir(n int) ([]iofs.DirEntry, error) {
	if d.entries == nil {
		d.entries = make([]iofs.DirEntry, 0, len(d.fs.files))
		for name, fd := range d.fs.files {
			d.entries = append(d.entries, &staticDirEntry{name: name, mode: fd.mode})
		}
	}
	if d.pos >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 || d.pos+n > len(d.entries) {
		n = len(d.entries) - d.pos
	}
	entries := d.entries[d.pos : d.pos+n]
	d.pos += n
	if d.pos >= len(d.entries) {
		return entries, io.EOF
	}
	return entries, nil
}

type readerOnlyFile struct {
	data []byte
	mode iofs.FileMode
	name string
	pos  int
}

func (f *readerOnlyFile) Stat() (iofs.FileInfo, error) {
	return &staticFileInfo{name: f.name, size: int64(len(f.data)), mode: f.mode}, nil
}

// Read implements io.Reader only — no ReaderAt, no Seeker.
func (f *readerOnlyFile) Read(dest []byte) (int, error) {
	if f.pos >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(dest, f.data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *readerOnlyFile) Close() error { return nil }

// readerAtFS is an fs.FS whose files implement io.ReaderAt but not Fd().
// This exercises the io.ReaderAt fallback path in fileHandle.Read.
type readerAtFS struct {
	files map[string]*readerOnlyFileData
}

func (f *readerAtFS) Open(name string) (iofs.File, error) {
	if name == "." {
		return &readerAtDir{fs: f}, nil
	}
	fd, ok := f.files[name]
	if !ok {
		return nil, &iofs.PathError{Op: "open", Path: name, Err: iofs.ErrNotExist}
	}
	return &readerAtFile{data: fd.data, mode: fd.mode, name: name}, nil
}

type readerAtDir struct {
	fs      *readerAtFS
	entries []iofs.DirEntry
	pos     int
}

func (d *readerAtDir) Stat() (iofs.FileInfo, error) {
	return &staticFileInfo{name: ".", size: 0, mode: iofs.ModeDir | 0555}, nil
}

func (d *readerAtDir) Read([]byte) (int, error) { return 0, io.EOF }
func (d *readerAtDir) Close() error             { return nil }

func (d *readerAtDir) ReadDir(n int) ([]iofs.DirEntry, error) {
	if d.entries == nil {
		d.entries = make([]iofs.DirEntry, 0, len(d.fs.files))
		for name, fd := range d.fs.files {
			d.entries = append(d.entries, &staticDirEntry{name: name, mode: fd.mode})
		}
	}
	if d.pos >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 || d.pos+n > len(d.entries) {
		n = len(d.entries) - d.pos
	}
	entries := d.entries[d.pos : d.pos+n]
	d.pos += n
	if d.pos >= len(d.entries) {
		return entries, io.EOF
	}
	return entries, nil
}

type readerAtFile struct {
	data []byte
	mode iofs.FileMode
	name string
}

func (f *readerAtFile) Stat() (iofs.FileInfo, error) {
	return &staticFileInfo{name: f.name, size: int64(len(f.data)), mode: f.mode}, nil
}

func (f *readerAtFile) Read(dest []byte) (int, error) {
	return 0, io.EOF // not used; ReaderAt path should be taken
}

func (f *readerAtFile) ReadAt(dest []byte, off int64) (int, error) {
	if int(off) >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(dest, f.data[off:])
	if int(off)+n >= len(f.data) {
		return n, io.EOF
	}
	return n, nil
}

func (f *readerAtFile) Close() error { return nil }

// staticFileInfo is a minimal fs.FileInfo.
type staticFileInfo struct {
	name string
	size int64
	mode iofs.FileMode
}

func (fi *staticFileInfo) Name() string        { return fi.name }
func (fi *staticFileInfo) Size() int64         { return fi.size }
func (fi *staticFileInfo) Mode() iofs.FileMode { return fi.mode }
func (fi *staticFileInfo) ModTime() time.Time  { return time.Time{} }
func (fi *staticFileInfo) IsDir() bool         { return fi.mode.IsDir() }
func (fi *staticFileInfo) Sys() any            { return nil }

// staticDirEntry is a minimal fs.DirEntry.
type staticDirEntry struct {
	name string
	mode iofs.FileMode
}

func (de *staticDirEntry) Name() string        { return de.name }
func (de *staticDirEntry) IsDir() bool         { return de.mode.IsDir() }
func (de *staticDirEntry) Type() iofs.FileMode { return de.mode.Type() }
func (de *staticDirEntry) Info() (iofs.FileInfo, error) {
	return &staticFileInfo{name: de.name, mode: de.mode}, nil
}

func TestReadSequentialOnly(t *testing.T) {
	// Test with an fs.FS whose files only implement io.Reader.
	content := []byte("sequential read only content")
	fsys := &readerOnlyFS{
		files: map[string]*readerOnlyFileData{
			"seq.txt": {data: content, mode: 0644},
		},
	}

	mountpoint := mountFS(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "seq.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestReadSequentialOnlyLargeFile(t *testing.T) {
	// 1MB file with sequential-only reader, exercises multiple FUSE READ calls.
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 256)
	}

	fsys := &readerOnlyFS{
		files: map[string]*readerOnlyFileData{
			"large.bin": {data: data, mode: 0644},
		},
	}

	mountpoint := mountFS(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("large file content mismatch: got %d bytes, want %d bytes", len(got), len(data))
	}
}

func TestReadReaderAt(t *testing.T) {
	// os.DirFS returns *os.File which implements Fd() — this now exercises
	// the splice/dupFd path. Test with a custom fs.FS whose files implement
	// io.ReaderAt but not Fd() to exercise the ReaderAt fallback path.
	content := []byte("readerat content here")
	fsys := &readerAtFS{
		files: map[string]*readerOnlyFileData{
			"ra.txt": {data: content, mode: 0644},
		},
	}

	mountpoint := mountFS(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "ra.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestReadReaderAtLargeFile(t *testing.T) {
	// 1MB file via readerAtFS to exercise ReaderAt path across multiple FUSE READ calls.
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 256)
	}

	fsys := &readerAtFS{
		files: map[string]*readerOnlyFileData{
			"large.bin": {data: data, mode: 0644},
		},
	}

	mountpoint := mountFS(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("large file content mismatch: got %d bytes, want %d bytes", len(got), len(data))
	}
}

// seekerFS is an fs.FS whose files implement io.ReadSeeker but not io.ReaderAt or Fd().
// This exercises the io.Seeker path in fileHandle.Read.
type seekerFS struct {
	files map[string]*readerOnlyFileData
}

func (f *seekerFS) Open(name string) (iofs.File, error) {
	if name == "." {
		return &seekerDir{fs: f}, nil
	}
	fd, ok := f.files[name]
	if !ok {
		return nil, &iofs.PathError{Op: "open", Path: name, Err: iofs.ErrNotExist}
	}
	return &seekerFile{data: fd.data, mode: fd.mode, name: name}, nil
}

type seekerDir struct {
	fs      *seekerFS
	entries []iofs.DirEntry
	pos     int
}

func (d *seekerDir) Stat() (iofs.FileInfo, error) {
	return &staticFileInfo{name: ".", size: 0, mode: iofs.ModeDir | 0555}, nil
}

func (d *seekerDir) Read([]byte) (int, error) { return 0, io.EOF }
func (d *seekerDir) Close() error             { return nil }

func (d *seekerDir) ReadDir(n int) ([]iofs.DirEntry, error) {
	if d.entries == nil {
		d.entries = make([]iofs.DirEntry, 0, len(d.fs.files))
		for name, fd := range d.fs.files {
			d.entries = append(d.entries, &staticDirEntry{name: name, mode: fd.mode})
		}
	}
	if d.pos >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 || d.pos+n > len(d.entries) {
		n = len(d.entries) - d.pos
	}
	entries := d.entries[d.pos : d.pos+n]
	d.pos += n
	if d.pos >= len(d.entries) {
		return entries, io.EOF
	}
	return entries, nil
}

type seekerFile struct {
	data []byte
	mode iofs.FileMode
	name string
	pos  int
}

func (f *seekerFile) Stat() (iofs.FileInfo, error) {
	return &staticFileInfo{name: f.name, size: int64(len(f.data)), mode: f.mode}, nil
}

func (f *seekerFile) Read(dest []byte) (int, error) {
	if f.pos >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(dest, f.data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *seekerFile) Seek(offset int64, whence int) (int64, error) {
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = int64(f.pos) + offset
	case io.SeekEnd:
		newPos = int64(len(f.data)) + offset
	}
	if newPos < 0 {
		return 0, &iofs.PathError{Op: "seek", Path: f.name, Err: iofs.ErrInvalid}
	}
	f.pos = int(newPos)
	return newPos, nil
}

func (f *seekerFile) Close() error { return nil }

func TestReadSeeker(t *testing.T) {
	content := []byte("seeker content here")
	fsys := &seekerFS{
		files: map[string]*readerOnlyFileData{
			"seek.txt": {data: content, mode: 0644},
		},
	}

	mountpoint := mountFS(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "seek.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestReadSeekerLargeFile(t *testing.T) {
	// 1MB file via seekerFS to exercise Seeker path across multiple FUSE READ calls.
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 256)
	}

	fsys := &seekerFS{
		files: map[string]*readerOnlyFileData{
			"large.bin": {data: data, mode: 0644},
		},
	}

	mountpoint := mountFS(t, fsys)

	got, err := os.ReadFile(filepath.Join(mountpoint, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("large file content mismatch: got %d bytes, want %d bytes", len(got), len(data))
	}
}

// benchSizes defines the file sizes used across all read-path benchmarks.
var benchSizes = []struct {
	name string
	size int
}{
	{"4KB", 4 << 10},
	{"1MB", 1 << 20},
	{"16MB", 16 << 20},
}

func makeTestData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	return data
}

func BenchmarkReadReaderAt(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			data := makeTestData(sz.size)
			fsys := &readerAtFS{
				files: map[string]*readerOnlyFileData{
					"bench.bin": {data: data, mode: 0644},
				},
			}

			mountpoint := mountFS(b, fsys)
			p := filepath.Join(mountpoint, "bench.bin")

			buf := make([]byte, 128<<10)
			b.SetBytes(int64(sz.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f, err := os.Open(p)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.CopyBuffer(io.Discard, f, buf); err != nil {
					b.Fatal(err)
				}
				f.Close()
			}
		})
	}
}

func BenchmarkReadSeeker(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			data := makeTestData(sz.size)
			fsys := &seekerFS{
				files: map[string]*readerOnlyFileData{
					"bench.bin": {data: data, mode: 0644},
				},
			}

			mountpoint := mountFS(b, fsys)
			p := filepath.Join(mountpoint, "bench.bin")

			buf := make([]byte, 128<<10)
			b.SetBytes(int64(sz.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f, err := os.Open(p)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.CopyBuffer(io.Discard, f, buf); err != nil {
					b.Fatal(err)
				}
				f.Close()
			}
		})
	}
}

func BenchmarkReadSequential(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			data := makeTestData(sz.size)
			fsys := &readerOnlyFS{
				files: map[string]*readerOnlyFileData{
					"bench.bin": {data: data, mode: 0644},
				},
			}

			mountpoint := mountFS(b, fsys)
			p := filepath.Join(mountpoint, "bench.bin")

			buf := make([]byte, 128<<10)
			b.SetBytes(int64(sz.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f, err := os.Open(p)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.CopyBuffer(io.Discard, f, buf); err != nil {
					b.Fatal(err)
				}
				f.Close()
			}
		})
	}
}
