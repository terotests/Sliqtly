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
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// the package's version (packaging/build-deb.sh sets it); "dev" elsewhere
var version = "dev"

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

	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	sw := &switchHandler{}
	srv := &http.Server{Addr: ":" + *port, Handler: sw, ReadHeaderTimeout: 10 * time.Second}
	serve := func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}
	if *data != "" && env("SLIQTLY_STORE", "") != "link" {
		// the folder is locked and brought to this version's format before
		// anything reads it; meanwhile the port answers "being updated"
		// (localstatus.go), so open pages wait instead of losing the server
		board := newStatusBoard("migrating", version)
		sw.set(maintenance(board))
		go serve()
		release, err := prepareData(*data, version, func(m string) { log.Print(m) })
		if err != nil {
			log.Printf("data folder %s: %v", *data, err)
			// the reason is in the log; the pages are told only that it failed
			board.set("failed", "")
			<-stop.Done()
			shutdown(srv)
			os.Exit(1)
		}
		defer release()
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
		ls := newLocalServer(e, bucket, *token, page).(*localServer)
		ls.board = board
		kind := fmt.Sprintf("folder %s, %s", *data, e.BaseURL)
		if page == nil {
			kind += ", no editor (npm run build, then go generate)"
		}
		sw.set(ls)
		board.set("ready", "")
		log.Printf("Sliqtly MCP %s (%s) on :%s, ready in %s", version, kind, *port, time.Since(start).Round(time.Microsecond))
		<-stop.Done()
		// pages hear it before the stream closes, and keep their edits
		board.set("stopping", "")
		shutdown(srv)
		return
	}
	var handler http.Handler
	kind := "link"
	{
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
	sw.set(handler)
	go serve()
	log.Printf("Sliqtly MCP %s (%s) on :%s, ready in %s", version, kind, *port, time.Since(start).Round(time.Microsecond))
	<-stop.Done()
	shutdown(srv)
}

// shutdown lets the requests under way finish (a save among them), for up
// to ten seconds
func shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Print("stopped")
}
