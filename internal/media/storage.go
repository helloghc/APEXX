// Package media stores the images of each store and produces the resized
// renditions the storefront serves with srcset.
//
// Files live behind the Storage interface, always addressed by tenant and
// key, so one store can never read another store's files. Two
// implementations exist:
//
//   - Local keeps files on disk under <root>/<tenant>/<key>. It is the default
//     and is right for a single node or a shared volume.
//   - GCS is a documented stub for Google Cloud Storage (see gcs.go). Use it
//     when several nodes serve the same stores.
package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("media: not found")

// ErrBadKey is returned for keys or tenants that could escape the tenant folder.
var ErrBadKey = errors.New("media: invalid key")

// Object is a stored file opened for reading.
type Object struct {
	io.ReadSeekCloser
	Size    int64
	ModTime time.Time
}

// Storage is the per-tenant object store of the media library.
type Storage interface {
	// Put writes data under key, replacing any previous content.
	Put(ctx context.Context, tenant, key string, data []byte, contentType string) error
	// Open returns the object stored under key or ErrNotFound.
	Open(ctx context.Context, tenant, key string) (*Object, error)
	// DeletePrefix removes every object whose key starts with prefix + "/".
	DeletePrefix(ctx context.Context, tenant, prefix string) error
}

var (
	tenantRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	keyRe    = regexp.MustCompile(`^[a-z0-9_]{1,40}(/[a-z0-9_-]{1,40}\.(jpg|png|webp|gif|bin))?$`)
)

// ValidKey reports whether tenant and key are safe to use as a path.
func ValidKey(tenant, key string) bool {
	return tenantRe.MatchString(tenant) && keyRe.MatchString(key) && !strings.Contains(key, "..")
}

// Local stores files on the local disk.
type Local struct {
	Root string
}

// NewLocal creates the root directory if needed.
func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("media dir: %w", err)
	}
	return &Local{Root: abs}, nil
}

func (l *Local) path(tenant, key string) (string, error) {
	if !ValidKey(tenant, key) {
		return "", ErrBadKey
	}
	return filepath.Join(l.Root, tenant, filepath.FromSlash(key)), nil
}

// Put writes atomically: a temporary file is renamed over the destination.
func (l *Local) Put(_ context.Context, tenant, key string, data []byte, _ string) error {
	p, err := l.path(tenant, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".up-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Open opens a stored file.
func (l *Local) Open(_ context.Context, tenant, key string) (*Object, error) {
	p, err := l.path(tenant, key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		_ = f.Close()
		return nil, ErrNotFound
	}
	return &Object{ReadSeekCloser: f, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// DeletePrefix removes the folder of one media item.
func (l *Local) DeletePrefix(_ context.Context, tenant, prefix string) error {
	p, err := l.path(tenant, prefix)
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}
