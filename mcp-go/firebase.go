//go:build !nocloud

// The real Firestore, Storage and Firebase Auth behind the DB and Bucket
// interfaces (store.go). Left out with -tags nocloud (nocloud.go), which
// drops Google's client libraries, about half the binary, from a server
// that keeps its decks in a folder.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/terotests/sliqtly/mcp-go/store"
)

type firestoreDB struct{ c *firestore.Client }

// presence/{deck}/agents/{agent} (presencecloud.go)
type fsPresence struct{ c *firestore.Client }

func (p fsPresence) put(ctx context.Context, deck, agent string, d Doc) error {
	_, err := p.c.Collection("presence").Doc(deck).Collection("agents").Doc(agent).Set(ctx, d, firestore.MergeAll)
	return err
}

func (p fsPresence) drop(ctx context.Context, deck, agent string) error {
	_, err := p.c.Collection("presence").Doc(deck).Collection("agents").Doc(agent).Delete(ctx)
	return err
}

func (d firestoreDB) Get(ctx context.Context, col, id string) (Doc, error) {
	snap, err := d.c.Collection(col).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return snap.Data(), nil
}

func (d firestoreDB) Set(ctx context.Context, col, id string, doc Doc) error {
	_, err := d.c.Collection(col).Doc(id).Set(ctx, doc)
	return err
}

func (d firestoreDB) Update(ctx context.Context, col, id string, doc Doc) error {
	ups := []firestore.Update{}
	for k, v := range doc {
		ups = append(ups, firestore.Update{Path: k, Value: v})
	}
	_, err := d.c.Collection(col).Doc(id).Update(ctx, ups)
	return err
}

func (d firestoreDB) Delete(ctx context.Context, col, id string) error {
	_, err := d.c.Collection(col).Doc(id).Delete(ctx)
	return err
}

func (d firestoreDB) Take(ctx context.Context, col, id string) (Doc, error) {
	ref := d.c.Collection(col).Doc(id)
	var got Doc
	err := d.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		got = nil
		snap, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		got = snap.Data()
		return tx.Delete(ref)
	})
	if err != nil {
		return nil, err
	}
	return got, nil
}

func (d firestoreDB) WhereEq(ctx context.Context, col, field string, value any) ([]Doc, []string, error) {
	return d.where(ctx, col, field, "==", value)
}

func (d firestoreDB) WhereHas(ctx context.Context, col, field string, value any) ([]Doc, []string, error) {
	return d.where(ctx, col, field, "array-contains", value)
}

func (d firestoreDB) where(ctx context.Context, col, field, op string, value any) ([]Doc, []string, error) {
	it := d.c.Collection(col).Where(field, op, value).Documents(ctx)
	defer it.Stop()
	docs, ids := []Doc{}, []string{}
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return docs, ids, nil
		}
		if err != nil {
			return nil, nil, err
		}
		docs = append(docs, snap.Data())
		ids = append(ids, snap.Ref.ID)
	}
}

func (firestoreDB) ServerTime() any { return firestore.ServerTimestamp }

func (d firestoreDB) Create(ctx context.Context, col, id string, doc Doc) (Doc, error) {
	ref := d.c.Collection(col).Doc(id)
	_, err := ref.Create(ctx, doc)
	if status.Code(err) != codes.AlreadyExists {
		return nil, err
	}
	snap, err := ref.Get(ctx)
	if err != nil {
		return nil, err
	}
	return snap.Data(), nil
}

func (d firestoreDB) UpdateIf(ctx context.Context, col, id, field, want string, doc Doc) (bool, error) {
	ref := d.c.Collection(col).Doc(id)
	ok := false
	err := d.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ok = false
		snap, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			if want != "" {
				return nil
			}
			ok = true
			return tx.Set(ref, doc)
		}
		if err != nil {
			return err
		}
		if fieldText(snap.Data(), field) != want {
			return nil
		}
		ok = true
		ups := []firestore.Update{}
		for k, v := range doc {
			ups = append(ups, firestore.Update{Path: k, Value: v})
		}
		return tx.Update(ref, ups)
	})
	return ok, err
}

// the leaves as firestore.Increment, merged into the document: map keys are
// field names, so a referrer's "example.com" stays one field
func (d firestoreDB) Increment(ctx context.Context, col, id string, add Doc) error {
	var inc func(m Doc) Doc
	inc = func(m Doc) Doc {
		out := Doc{}
		for k, v := range m {
			if sub, ok := v.(map[string]any); ok {
				out[k] = inc(sub)
			} else {
				out[k] = firestore.Increment(v)
			}
		}
		return out
	}
	_, err := d.c.Collection(col).Doc(id).Set(ctx, inc(add), firestore.MergeAll)
	return err
}

type gcsBucket struct{ b *storage.BucketHandle }

func (g gcsBucket) Name() string { return g.b.BucketName() }

func (g gcsBucket) Save(ctx context.Context, path, contentType string, data []byte, metadata map[string]string) error {
	w := g.b.Object(path).NewWriter(ctx)
	w.ContentType = contentType
	w.Metadata = metadata
	w.ChunkSize = 0 // one request, as resumable: false
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (g gcsBucket) Read(ctx context.Context, path string, limit int64) ([]byte, error) {
	r, err := g.b.Object(path).NewReader(ctx)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, limit))
}

func (g gcsBucket) RemovePrefix(ctx context.Context, prefix string) error {
	it := g.b.Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := g.b.Object(a.Name).Delete(ctx); err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
			return err
		}
	}
}

// the rest of what the shared files need (cloudshared.go)

func (g gcsBucket) SaveFrom(ctx context.Context, path, contentType string, r io.Reader) (store.BlobInfo, error) {
	w := g.b.Object(path).NewWriter(ctx)
	w.ContentType = contentType
	n, err := io.Copy(w, r)
	if err != nil {
		w.Close()
		return store.BlobInfo{}, err
	}
	if err := w.Close(); err != nil {
		return store.BlobInfo{}, err
	}
	var h store.Hash
	copy(h[:], w.Attrs().MD5)
	return store.BlobInfo{Hash: h, Size: n, Mime: contentType, Created: w.Attrs().Created}, nil
}

// the objects under dir/
func (g gcsBucket) List(ctx context.Context, dir string) ([]store.FileRef, error) {
	it := g.b.Objects(ctx, &storage.Query{Prefix: strings.TrimSuffix(dir, "/") + "/"})
	out := []store.FileRef{}
	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, store.FileRef{Path: a.Name, Size: a.Size, Mime: a.ContentType, Updated: a.Updated})
	}
}

func (g gcsBucket) Remove(path string) error {
	err := g.b.Object(path).Delete(context.Background())
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	return err
}

func (g gcsBucket) RemoveAll(path string) error {
	return g.RemovePrefix(context.Background(), strings.TrimSuffix(path, "/")+"/")
}

// Move copies `from` to `to` in Storage (no byte through here) and
// removes `from` → the size and MD5
func (g gcsBucket) Move(ctx context.Context, from, to string) (int64, string, error) {
	a, err := g.b.Object(to).CopierFrom(g.b.Object(from)).Run(ctx)
	if err != nil {
		return 0, "", err
	}
	g.Remove(from)
	return a.Size, fmt.Sprintf("%x", a.MD5), nil
}

// The Firebase project's Firestore, Storage and Auth, with Application
// Default Credentials (the Cloud Run service account).
func connectFirebase(ctx context.Context, env *Env, projectID, bucket string) error {
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID, StorageBucket: bucket})
	if err != nil {
		return err
	}
	fs, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	st, err := app.Storage(ctx)
	if err != nil {
		return err
	}
	b, err := st.Bucket(bucket)
	if err != nil {
		return err
	}
	auth, err := app.Auth(ctx)
	if err != nil {
		return err
	}
	env.DB = firestoreDB{fs}
	env.Bucket = gcsBucket{b}
	env.OAuth = true
	env.VerifyIDToken = func(ctx context.Context, idToken string) (*IDToken, error) {
		t, err := auth.VerifyIDToken(ctx, idToken)
		if err != nil {
			return nil, err
		}
		name, _ := t.Claims["name"].(string)
		email, _ := t.Claims["email"].(string)
		verified, _ := t.Claims["email_verified"].(bool)
		return &IDToken{UID: t.UID, Name: name, Email: email, Verified: verified}, nil
	}
	// each account's own Figma files (cloudshared.go), read by the job
	// SLIQTLY_FIGMA_JOB names
	env.cloudShared = newCloudShared(gcsBucket{b}, cloudJobStarter(os.Getenv("SLIQTLY_FIGMA_JOB")))
	// shared rooms and their chat for the editor (editorrooms.go)
	env.cloudRooms = newCloudRoomService(env, newFSEngine(fs, ""), newFSChat(fs, ""))
	// who is working on the editor's Stories (presencecloud.go)
	env.agents.pub = newPresencePub(fsPresence{fs})
	// the owner's dashboard (admin.go), for the accounts listed
	env.Admin = newAdminConfig(splitList(os.Getenv("SLIQTLY_ADMIN_EMAILS")), &cloudAdmin{fs: fs, auth: auth, project: projectID, table: os.Getenv("SLIQTLY_BILLING_TABLE")})
	return nil
}
