// The real Firestore, Storage and Firebase Auth behind the DB and Bucket
// interfaces (store.go).

package main

import (
	"context"
	"errors"
	"io"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type firestoreDB struct{ c *firestore.Client }

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

func (d firestoreDB) WhereEq(ctx context.Context, col, field string, value any) ([]Doc, []string, error) {
	it := d.c.Collection(col).Where(field, "==", value).Documents(ctx)
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
		return &IDToken{UID: t.UID, Name: name, Email: email}, nil
	}
	return nil
}
