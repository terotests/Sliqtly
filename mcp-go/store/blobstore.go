// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

// BlobStore keeps immutable bytes by their SHA-256: the same bytes are
// kept once, whatever their names. It knows nothing of who may read what:
// a hash is not a permission. What a path or a deck holds is a file
// reference in the documents (FileRefs), and access is decided there.
//
//	SQLiteBlobStore   blobs.db beside sliqtly.db, in chunks (the default)
//	FSBlobStore       one file per blob under a folder
//
// and a later S3BlobStore for a deployment on PostgreSQL. RangerDiff's
// versions (RdRepo) use the same ids, so a deck's history can keep its
// objects here.
type BlobStore interface {
	// Put reads r to its end and keeps the bytes; the same bytes again
	// return the same Hash and keep nothing new.
	Put(ctx context.Context, r io.Reader, mime string) (BlobInfo, error)
	// Open reads a blob from any offset without holding it in memory.
	Open(ctx context.Context, h Hash) (BlobReader, error)
	Stat(ctx context.Context, h Hash) (BlobInfo, error)
	// Delete removes a blob. Only the collector (CollectBlobs) calls it:
	// a blob may be named by many references.
	Delete(ctx context.Context, h Hash) error
	// Each calls fn with every blob, in hash order.
	Each(ctx context.Context, fn func(BlobInfo) error) error
	Close() error
}

// DeltaBlobStore keeps a blob as a delta against another one when that
// pays (SQLiteBlobStore): its hash and bytes stay the same.
type DeltaBlobStore interface {
	BlobStore
	Deltify(ctx context.Context, h, base Hash) (bool, error)
}

// BlobReader is a blob's bytes: http.ServeContent takes it as it is, and
// a Range request reads only the chunks it covers.
type BlobReader interface {
	io.ReadSeekCloser
	io.ReaderAt
	Info() BlobInfo
}

// Hash is a blob's SHA-256.
type Hash [32]byte

func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// ParseHash reads a hash written as 64 hex digits.
func ParseHash(s string) (Hash, error) {
	var h Hash
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		return h, fmt.Errorf("store: bad blob hash %q", s)
	}
	copy(h[:], b)
	return h, nil
}

// HashOf is the hash of b.
func HashOf(b []byte) Hash { return sha256.Sum256(b) }

type BlobInfo struct {
	Hash    Hash
	Size    int64
	Mime    string
	Created time.Time
}

// ErrNoBlob: no blob has that hash
var ErrNoBlob = errors.New("store: no such blob")

// VerifyBlobs reads every blob of b and checks its bytes against its hash
// and size. → the blobs that did not match
func VerifyBlobs(ctx context.Context, b BlobStore) (checked int, bad []Hash, err error) {
	err = b.Each(ctx, func(info BlobInfo) error {
		r, err := b.Open(ctx, info.Hash)
		if err != nil {
			return err
		}
		defer r.Close()
		h := sha256.New()
		n, err := io.Copy(h, r)
		if err != nil {
			return fmt.Errorf("%s: %w", info.Hash, err)
		}
		checked++
		var got Hash
		copy(got[:], h.Sum(nil))
		if got != info.Hash || n != info.Size {
			bad = append(bad, info.Hash)
		}
		return nil
	})
	return checked, bad, err
}

// CollectBlobs deletes the blobs no reference names that were made before
// `before` (a blob put a moment ago may be about to get its reference:
// it is written first, the reference after). → how many went
func CollectBlobs(ctx context.Context, b BlobStore, referenced func(Hash) (bool, error), before time.Time) (int, error) {
	var gone []Hash
	err := b.Each(ctx, func(info BlobInfo) error {
		if !info.Created.Before(before) {
			return nil
		}
		ok, err := referenced(info.Hash)
		if err != nil {
			return err
		}
		if !ok {
			gone = append(gone, info.Hash)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, h := range gone {
		if err := b.Delete(ctx, h); err != nil {
			return 0, err
		}
	}
	if s, ok := b.(interface{ Shrink(context.Context) error }); ok && len(gone) > 0 {
		if err := s.Shrink(ctx); err != nil {
			return len(gone), err
		}
	}
	return len(gone), nil
}

// readerAtSeeker is the Read and Seek of a BlobReader on its ReadAt
type readerAtSeeker struct {
	at   io.ReaderAt
	size int64
	off  int64
}

func (r *readerAtSeeker) Read(p []byte) (int, error) {
	if r.off >= r.size {
		return 0, io.EOF
	}
	if rest := r.size - r.off; int64(len(p)) > rest {
		p = p[:rest]
	}
	n, err := r.at.ReadAt(p, r.off)
	r.off += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

func (r *readerAtSeeker) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		off += r.off
	case io.SeekEnd:
		off += r.size
	default:
		return 0, errors.New("store: bad whence")
	}
	if off < 0 {
		return 0, errors.New("store: seek before the start")
	}
	r.off = off
	return off, nil
}
