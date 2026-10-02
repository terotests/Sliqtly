// The Sliqtly MCP server for Cloud Run: one static binary, listening on
// $PORT. The server is Ranger (rgr/, compiled to sliqtly_mcp.go); this file,
// host.go, net.go and firebase.go are the Go around it.
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
	e := &Env{BaseURL: env("SLIQTLY_URL", "https://sliqtly.com"), Client: newPublicClient()}
	cloud := os.Getenv("K_SERVICE") != "" || os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" || os.Getenv("FIRESTORE_EMULATOR_HOST") != ""
	if env("SLIQTLY_STORE", "") == "link" {
		cloud = false
	}
	kind := "link"
	if cloud {
		err := connectFirebase(context.Background(), e, env("GOOGLE_CLOUD_PROJECT", env("GCLOUD_PROJECT", "sliqtly")), env("SLIQTLY_BUCKET", "sliqtly.firebasestorage.app"))
		if err != nil {
			log.Fatalf("firebase: %v", err)
		}
		kind = "cloud"
	} else {
		e.TrustHost = true
	}
	port := env("PORT", "8080")
	srv := &http.Server{Addr: ":" + port, Handler: NewApp(e), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Sliqtly MCP (%s) on :%s, ready in %s", kind, port, time.Since(start).Round(time.Microsecond))
	log.Fatal(srv.ListenAndServe())
}
