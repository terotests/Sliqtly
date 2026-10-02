// The Sliqtly MCP server for Cloud Run: one static binary, listening on
// $PORT. A Go port of mcp/ (the Cloud Function), with the same tools, the same
// Firestore documents and Storage paths, and the same OAuth endpoints.
//
// On Cloud Run (K_SERVICE set) or with GOOGLE_APPLICATION_CREDENTIALS it
// writes real shares and offers sign-in. Otherwise, as `node local.js`, the
// deck travels in the link (#md=…) and no pictures are kept.

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	start := time.Now()
	ctx := context.Background()
	port := env("PORT", "8080")
	baseURL := env("SLIQTLY_URL", "https://sliqtly.com")
	client := newPublicClient()

	opts := AppOpts{Store: LinkStore{}, BaseURL: baseURL, Client: client}
	cloud := os.Getenv("K_SERVICE") != "" || os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" || os.Getenv("FIRESTORE_EMULATOR_HOST") != ""
	if env("SLIQTLY_STORE", "") == "link" {
		cloud = false
	}
	if cloud {
		store, verify, db, err := connectFirebase(ctx, env("GOOGLE_CLOUD_PROJECT", env("GCLOUD_PROJECT", "sliqtly")), env("SLIQTLY_BUCKET", "sliqtly.firebasestorage.app"))
		if err != nil {
			log.Fatalf("firebase: %v", err)
		}
		opts.Store = store
		opts.OAuth = &OAuth{DB: db, VerifyIDToken: verify, Client: client}
	} else {
		opts.TrustHost = true
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           NewApp(opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("Sliqtly MCP (%s) on :%s, ready in %s", opts.Store.Kind(), port, time.Since(start).Round(time.Microsecond))
	log.Fatal(srv.ListenAndServe())
}
