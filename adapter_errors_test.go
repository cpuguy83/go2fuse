package go2fuse_test

import (
	"errors"
	"fmt"
	iofs "io/fs"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/cpuguy83/go2fuse"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestLookupErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		want syscall.Errno
	}{
		{
			name: "not-exist-is-enoent",
			err:  iofs.ErrNotExist,
			want: syscall.ENOENT,
		},
		{
			name: "exist-is-eexist",
			err:  iofs.ErrExist,
			want: syscall.EEXIST,
		},
		{
			name: "permission-is-eperm",
			err:  iofs.ErrPermission,
			want: syscall.EPERM,
		},
		{
			name: "invalid-is-einval",
			err:  iofs.ErrInvalid,
			want: syscall.EINVAL,
		},
		{
			name: "preserve-access-denied",
			err:  syscall.EACCES,
			want: syscall.EACCES,
		},
		{
			name: "preserve-directory-not-empty",
			err:  syscall.ENOTEMPTY,
			want: syscall.ENOTEMPTY,
		},
		{
			name: "preserve-io-error",
			err:  syscall.EIO,
			want: syscall.EIO,
		},
		{
			name: "unknown-error-fallback",
			err:  errors.New("unknown error"),
			want: syscall.ENOSYS,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pathErr := &iofs.PathError{
				Op:   "open",
				Path: "file",
				Err:  tt.err,
			}
			for _, input := range []struct {
				name string
				err  error
			}{
				{
					name: "bare",
					err:  tt.err,
				},
				{
					name: "path-error",
					err:  pathErr,
				},
				{
					name: "wrapped-path-error",
					err:  fmt.Errorf("opening file: %w", pathErr),
				},
			} {
				t.Run(input.name, func(t *testing.T) {
					t.Parallel()

					root := go2fuse.New(errorFS{err: input.err})
					_, errno := root.(fs.NodeLookuper).Lookup(t.Context(), "file", &fuse.EntryOut{})
					if errno != tt.want {
						t.Errorf("Lookup error = %v, want %v", errno, tt.want)
					}
				})
			}
		})
	}
}

func TestLookupSuccess(t *testing.T) {
	t.Parallel()

	root := go2fuse.New(fstest.MapFS{
		"file": &fstest.MapFile{Mode: 0644},
	})
	fs.NewNodeFS(root, &fs.Options{})

	_, errno := root.(fs.NodeLookuper).Lookup(t.Context(), "file", &fuse.EntryOut{})
	if errno != 0 {
		t.Errorf("Lookup error = %v, want success", errno)
	}
}

type errorFS struct {
	err error
}

func (f errorFS) Open(string) (iofs.File, error) {
	return nil, f.err
}
