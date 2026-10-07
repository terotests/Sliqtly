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
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
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
	store.Build = version
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		os.Exit(backupCmd(os.Args[2:], os.Stdout, os.Stderr))
	}
	data := flag.String("data", env("SLIQTLY_DATA", ""), "keep decks in this folder (SLIQTLY_DATA)")
	port := flag.String("port", env("PORT", "8080"), "port to listen on (PORT)")
	base := flag.String("url", env("SLIQTLY_URL", ""), "the address people and links use, e.g. https://sliqtly.example.com (SLIQTLY_URL)")
	user := flag.String("user", env("SLIQTLY_USER", "local"), "the owner of the decks kept in the folder (SLIQTLY_USER)")
	token := flag.String("token", env("SLIQTLY_TOKEN", ""), "require Authorization: Bearer <token> on /mcp (SLIQTLY_TOKEN)")
	web := flag.String("web", env("SLIQTLY_WEB", ""), "serve the editor and player from this built web/dist instead of the copy built in (SLIQTLY_WEB)")
	listen := flag.String("listen", env("SLIQTLY_LISTEN", ""), "who can connect: local (this computer only, the default), wired (also computers on a wired network) or network (every interface); unset: the settings page decides (SLIQTLY_LISTEN)")
	allow := flag.String("allow", env("SLIQTLY_ALLOW", ""), "other computers' address ranges let in, e.g. 10.20.0.0/16 (SLIQTLY_ALLOW)")
	corsOrigins := flag.String("cors-origins", env("SLIQTLY_CORS_ORIGINS", ""), "pages of other origins that may call /api/v1 and /oauth/token, comma separated, e.g. https://editor.example.com; pages on this computer always may (SLIQTLY_CORS_ORIGINS)")
	tlsCert := flag.String("tls-cert", env("SLIQTLY_TLS_CERT", ""), "serve https:// with this certificate (PEM, the chain after it) instead of the server's own authority; with -tls-key (SLIQTLY_TLS_CERT)")
	tlsKey := flag.String("tls-key", env("SLIQTLY_TLS_KEY", ""), "the private key of -tls-cert (PEM) (SLIQTLY_TLS_KEY)")
	oidcIssuer := flag.String("oidc-issuer", env("SLIQTLY_OIDC_ISSUER", ""), "sign-in through this OpenID Connect provider, e.g. https://accounts.google.com (SLIQTLY_OIDC_ISSUER)")
	oidcClient := flag.String("oidc-client-id", env("SLIQTLY_OIDC_CLIENT_ID", ""), "this server's client id at the provider (SLIQTLY_OIDC_CLIENT_ID)")
	oidcSecret := flag.String("oidc-client-secret", env("SLIQTLY_OIDC_CLIENT_SECRET", ""), "its client secret, if the provider gave one; the environment keeps it out of the process list (SLIQTLY_OIDC_CLIENT_SECRET)")
	oidcAllow := flag.String("oidc-allow", env("SLIQTLY_OIDC_ALLOW", ""), "who may sign in: emails and @domains, comma separated, or * for every account the provider signs in (SLIQTLY_OIDC_ALLOW)")
	oidcScopes := flag.String("oidc-scopes", env("SLIQTLY_OIDC_SCOPES", "openid email profile"), "the scopes asked of the provider (SLIQTLY_OIDC_SCOPES)")
	flag.Parse()
	oidcCfg := oidcConfig{Issuer: *oidcIssuer, ClientID: *oidcClient, ClientSecret: *oidcSecret, Allow: splitList(*oidcAllow), Scopes: *oidcScopes}
	if err := oidcCfg.check(); err != nil {
		log.Fatal(err)
	}
	cors, err := newCORSPolicy(splitList(*corsOrigins))
	if err != nil {
		log.Fatal(err)
	}
	if (*tlsCert != "" || *tlsKey != "" || oidcCfg.on()) && *data == "" {
		log.Fatal("-tls-cert, -tls-key and -oidc-* are for a server of one's own: start it with -data")
	}

	// who can connect (netaccess.go): this computer only unless told
	// otherwise; Cloud Run's own front is the only way in there
	access := *listen
	if access == "" && os.Getenv("K_SERVICE") != "" {
		access = accessNetwork
	}
	policy, err := newNetPolicy(access, splitList(*allow), access != "" || *allow != "")
	if err != nil {
		log.Fatal(err)
	}

	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	sw := &switchHandler{}
	srv := &http.Server{Handler: sw, ReadHeaderTimeout: 10 * time.Second}
	expo := newExposure(srv, *port, policy)
	serve := func() {
		if err := expo.sync(true); err != nil {
			log.Fatal(err)
		}
		go expo.run(stop)
	}
	if *data != "" && env("SLIQTLY_STORE", "") != "link" {
		backups, err := backupFromEnv()
		if err == nil && backups.Repo != "" {
			err = checkBackupPlace(*data, backups.Repo)
		}
		if err != nil {
			log.Fatal(err)
		}
		// the folder is locked and brought to this version's format before
		// anything reads it; meanwhile the port answers "being updated"
		// (localstatus.go), so open pages wait instead of losing the server
		board := newStatusBoard("migrating", version)
		sw.set(maintenance(board))
		serve()
		release, err := prepareData(*data, version, *user, func(m string) { log.Print(m) })
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
		ls.expo = expo
		ls.cors = cors
		if err := ls.useOIDC(oidcCfg); err != nil {
			log.Fatal(err)
		}
		if oidcCfg.on() {
			log.Printf("sign-in through %s; register %s as the redirect URI there", oidcCfg.Issuer, ls.oidcRedirect())
		}
		// https:// with a certificate of the server's name when one is
		// given, else with the server's own, for microphones on other
		// computers
		if *tlsCert != "" || *tlsKey != "" {
			fc, err := loadFileCert(*tlsCert, *tlsKey, log.Printf)
			if err != nil {
				log.Fatalf("tls certificate: %v", err)
			}
			ls.fileCert = fc
			expo.tls.Store(fc.config())
		} else if certs, err := loadOwnCerts(filepath.Join(*data, "tls"), ls.hosts.list(), ls.hosts.hostOK); err != nil {
			log.Printf("own certificate: %v (https:// is off)", err)
		} else {
			ls.certs = certs
			expo.tls.Store(certs.config())
		}
		// who can connect, as the settings page last set it
		if !policy.Fixed {
			if p, err := loadNetPolicy(context.Background(), e.DB); err == nil {
				expo.setPolicy(p)
			}
		}
		kind := fmt.Sprintf("folder %s, %s", *data, e.BaseURL)
		if page == nil {
			kind += ", no editor (npm run build, then go generate)"
		}
		sw.set(ls)
		go ls.sweepExpired(stop)
		if backups.Repo != "" {
			go ls.backupLoop(stop, *data, backups)
		}
		board.set("ready", "")
		log.Printf("Sliqtly MCP %s (%s) on port %s, ready in %s", version, kind, *port, time.Since(start).Round(time.Microsecond))
		<-stop.Done()
		// pages hear it before the stream closes, and keep their edits
		board.set("stopping", "")
		shutdown(srv)
		// no edit can come in now: what the rooms took is written before
		// the folder is let go
		ls.flushRooms()
		return
	}
	var handler http.Handler
	kind := "link"
	{
		u := *base
		if u == "" {
			u = "https://sliqtly.com"
		}
		e := &Env{BaseURL: u, Client: newPublicClient(), GitHubToken: os.Getenv("SLIQTLY_GITHUB_TOKEN"), GitHubUsers: githubUsers(os.Getenv("SLIQTLY_GITHUB_USERS"))}
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
			e.Renders = dailyRenders(e.DB, 100, 500, time.Now)
		} else {
			e.TrustHost = true
		}
		handler = NewApp(e)
	}
	sw.set(handler)
	serve()
	log.Printf("Sliqtly MCP %s (%s) on port %s, ready in %s", version, kind, *port, time.Since(start).Round(time.Microsecond))
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
