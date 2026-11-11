package media

import (
	"context"
	"errors"
)

// ErrNotConfigured is returned by storage back ends that are declared but not
// wired in this build.
var ErrNotConfigured = errors.New("media: storage back end not configured")

// GCS is the Google Cloud Storage implementation of Storage. It is a stub:
// the type and its contract are fixed so the rest of the platform does not
// change when it is filled in, but every call returns ErrNotConfigured.
//
// How to complete it (kept out of this build to avoid a cloud dependency in
// the single-binary deployment):
//
//  1. Add cloud.google.com/go/storage and create one *storage.Client at
//     start-up (Application Default Credentials; the VM service account needs
//     roles/storage.objectAdmin on the bucket).
//  2. Object name: Prefix + tenant + "/" + key. Validate with ValidKey first,
//     exactly as Local does, so a key can never leave its tenant prefix.
//  3. Put: bucket.Object(name).NewWriter(ctx) with ContentType set and
//     CacheControl "public, max-age=31536000, immutable" (keys are never
//     rewritten with different content).
//  4. Open: bucket.Object(name).NewRangeReader is not seekable; either read
//     the object into memory (renditions are small) and wrap it in
//     bytes.Reader, or serve by redirecting to a signed URL / Cloud CDN and
//     skip Open on the hot path. Map storage.ErrObjectNotExist to ErrNotFound.
//  5. DeletePrefix: iterate bucket.Objects(ctx, &storage.Query{Prefix: name + "/"})
//     and delete each object.
//  6. Select it with APEXX_MEDIA_STORAGE=gcs and APEXX_MEDIA_BUCKET=<bucket>
//     in cmd/apexx (today any value other than "local" stops the server at
//     start-up with a clear message instead of failing on the first upload).
type GCS struct {
	Bucket string
	Prefix string
}

// Put is not implemented.
func (GCS) Put(context.Context, string, string, []byte, string) error { return ErrNotConfigured }

// Open is not implemented.
func (GCS) Open(context.Context, string, string) (*Object, error) { return nil, ErrNotConfigured }

// DeletePrefix is not implemented.
func (GCS) DeletePrefix(context.Context, string, string) error { return ErrNotConfigured }
