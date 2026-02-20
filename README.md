# go2fuse

Adapt any Go `io/fs.FS` into a read-only FUSE filesystem.

## Usage

```go
package main

import (
	"log"
	"os"
	"time"

	"github.com/cpuguy83/go2fuse"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func main() {
	fsys := os.DirFS("/some/directory")

	sec := time.Second
	server, err := go2fuse.Mount("/mnt/point", fsys, &fs.Options{
		MountOptions: fuse.MountOptions{
			FsName: "myfs",
			Name:   "myfs",
		},
		EntryTimeout: &sec,
		AttrTimeout:  &sec,
	})
	if err != nil {
		log.Fatal(err)
	}

	server.Wait()
}
```

Or use `New()` to get the root node directly for more control:

```go
root := go2fuse.New(fsys)
server, err := fs.Mount("/mnt/point", root, opts)
```

## How it works

The adapter lazily maps FUSE operations to `io/fs` calls:

| FUSE operation | io/fs call |
|----------------|------------|
| Lookup | `fs.Stat` |
| Getattr | `fs.Stat` |
| Readdir | `fs.ReadDir` |
| Open | `fsys.Open` |
| Read | `io.ReaderAt` / `io.Seeker` / `io.Reader` (tried in order) |

All write operations are rejected with `EROFS`.

## Limitations

- **Read-only** — matches `io/fs.FS` semantics, no write support.
- **No symlink targets** — `io/fs.FS` has no `Readlink` equivalent. Symlinks are detected in mode bits but targets cannot be resolved.
- **Sequential-only readers** — `fs.File` implementations without `io.ReaderAt` or `io.Seeker` will have poor random-read performance since data must be read and discarded up to the requested offset.
