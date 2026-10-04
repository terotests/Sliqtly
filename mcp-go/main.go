// The Sliqtly MCP server: one static binary, listening on $PORT. The server
// is Ranger (rgr/, compiled to sliqtly_mcp.go); this file, host.go, net.go,
// firebase.go, fsstore.go and local.go are the Go around it.
//
// Where decks are kept:
//   - Firestore and Storage on Cloud Run (K_SERVICE set) or with
//     GOOGLE_APPLICATION_CREDENTIALS, with sign-in: sliqtly.com
//   - a folder, with -data or SLIQTLY_DATA: a server of one's own, no
//     sign-in, decks viewed at /s/{id} on this server (local.go)
//   - nowhere: the deck travels in the link (#md=…), SLIQTLY_STORE=link or
//     nothing set

package main

import (
	"context"
	"flag"
	"fmt"
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
	data := flag.String("data", env("SLIQTLY_DATA", ""), "keep decks in this folder (SLIQTLY_DATA)")
	port := flag.String("port", env("PORT", "8080"), "port to listen on (PORT)")
	base := flag.String("url", env("SLIQTLY_URL", ""), "the address people and links use, e.g. https://sliqtly.example.com (SLIQTLY_URL)")
	user := flag.String("user", env("SLIQTLY_USER", "local"), "the owner of the decks kept in the folder (SLIQTLY_USER)")
	token := flag.String("token", env("SLIQTLY_TOKEN", ""), "require Authorization: Bearer <token> on /mcp (SLIQTLY_TOKEN)")
	web := flag.String("web", env("SLIQTLY_WEB", ""), "serve the editor and player from this built web/dist instead of the copy built in (SLIQTLY_WEB)")
	flag.Parse()

	var handler http.Handler
	kind := "link"
	if *data != "" && env("SLIQTLY_STORE", "") != "link" {
		u := *base
		if u == "" {
			u = "http://localhost:" + *port
		}
		e, bucket, err := localEnv(*data, u, *user)
		if err != nil {
			log.Fatalf("data folder %s: %v", *data, err)
		}
		// links follow the address a request came in on unless the address is set
		e.TrustHost = *base == ""
		page := webFiles(*web)
		handler = newLocalServer(e, bucket, *token, page)
		kind = fmt.Sprintf("folder %s, %s", *data, e.BaseURL)
		if page == nil {
			kind += ", no editor (npm run build, then go generate)"
		}
	} else {
		u := *base
		if u == "" {
			u = "https://sliqtly.com"
		}
		e := &Env{BaseURL: u, Client: newPublicClient()}
		cloud := os.Getenv("K_SERVICE") != "" || os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" || os.Getenv("FIRESTORE_EMULATOR_HOST") != ""
		if env("SLIQTLY_STORE", "") == "link" {
			cloud = false
		}
		if cloud {
			err := connectFirebase(context.Background(), e, env("GOOGLE_CLOUD_PROJECT", env("GCLOUD_PROJECT", "sliqtly")), env("SLIQTLY_BUCKET", "sliqtly.firebasestorage.app"))
			if err != nil {
				log.Fatalf("firebase: %v", err)
			}
			kind = "cloud"
			e.Quota = dailyQuota(e.DB, 50, 500, time.Now)
		} else {
			e.TrustHost = true
		}
		handler = NewApp(e)
	}
	srv := &http.Server{Addr: ":" + *port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Sliqtly MCP (%s) on :%s, ready in %s", kind, *port, time.Since(start).Round(time.Microsecond))
	log.Fatal(srv.ListenAndServe())
}
