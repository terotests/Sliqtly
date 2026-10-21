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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/terotests/sliqtly/mcp-go/connectors"
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

// a yes/no setting from the environment: 1, true, yes or on
func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// a whole number from the environment, def when unset or not a number
func envInt(name string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil {
		return n
	}
	return def
}

func main() {
	start := time.Now()
	store.Build = version
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		os.Exit(backupCmd(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "git" {
		os.Exit(gitCmd(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "warden-morning" {
		os.Exit(wardenMorningCmd())
	}
	if len(os.Args) > 1 && os.Args[1] == "figma-index" {
		os.Exit(figmaIndexCmd(os.Args[2:]))
	}
	data := flag.String("data", env("SLIQTLY_DATA", ""), "keep decks in this folder (SLIQTLY_DATA)")
	port := flag.String("port", env("PORT", "8080"), "port to listen on (PORT)")
	base := flag.String("url", env("SLIQTLY_URL", ""), "the address people and links use, e.g. https://sliqtly.example.com (SLIQTLY_URL)")
	user := flag.String("user", env("SLIQTLY_USER", "local"), "the owner of the decks kept in the folder (SLIQTLY_USER)")
	token := flag.String("token", env("SLIQTLY_TOKEN", ""), "require this token: Authorization: Bearer <token> on /mcp and /api/v1, and on the rest of the server that or the cookie a browser gets with ?token=<token>; when other computers can connect (-listen wired or network) and this is unset, the server makes its own, kept in <data>/access-token (SLIQTLY_TOKEN)")
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
	importDirsFlag := flag.String("import-dirs", env("SLIQTLY_IMPORT_DIRS", ""), "folders of this computer MCP tools may read pictures and data files from by path, comma separated, e.g. /Users/me/photoalbum; with -data only (SLIQTLY_IMPORT_DIRS)")
	maxFileMB := flag.Int("max-file-mb", envInt("SLIQTLY_MAX_FILE_MB", localMaxFileMB), "with -data: the most megabytes one picture or file may have, sent by an agent, read from an import folder or added in the editor (SLIQTLY_MAX_FILE_MB)")
	connectorsOn := flag.Bool("connectors", envBool("SLIQTLY_CONNECTORS"), "let scripts and workflows in decks use the services set in <data>/connectors/*.json, with the grants the admin approves (SLIQTLY_CONNECTORS=1)")
	writeLimit := flag.Int("write-limit", envInt("SLIQTLY_WRITE_LIMIT", localWriteLimit), "with -data: MCP writes the whole server takes in 10 minutes, 0 for no limit; many agents working at once need more (SLIQTLY_WRITE_LIMIT)")
	adminsFlag := flag.String("admin", env("SLIQTLY_ADMINS", ""), "signed-in accounts (emails, comma separated) that may approve connector grants; the server's own user on its own computer always may (SLIQTLY_ADMINS)")
	flag.Parse()
	applyMemoryLimit()
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
	imports, err := parseImportDirs(*importDirsFlag)
	if err != nil {
		log.Fatal(err)
	}
	if len(imports) > 0 && (*data == "" || env("SLIQTLY_STORE", "") == "link" || os.Getenv("K_SERVICE") != "") {
		log.Fatal("-import-dirs (SLIQTLY_IMPORT_DIRS) is for a server of one's own: start it with -data")
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
		e.ImportDirs = imports
		if *maxFileMB > 0 {
			e.MaxFile = int64(*maxFileMB) << 20
		}
		e.Limiter = localLimiter(*writeLimit)
		if len(imports) > 0 {
			log.Printf("MCP tools may read files by path from %s", strings.Join(imports.list(), ", "))
		}
		// links follow the address a request came in on unless the address is set
		e.TrustHost = *base == ""
		page := webFiles(*web)
		ls := newLocalServer(e, bucket, *token, page).(*localServer)
		ls.board = board
		ls.expo = expo
		ls.cors = cors
		ls.tokenFile = accessTokenPath(*data)
		if err := ls.useOIDC(oidcCfg); err != nil {
			log.Fatal(err)
		}
		ls.admins = splitList(*adminsFlag)
		if *connectorsOn {
			g, err := connectors.Open(filepath.Join(*data, "connectors"), newPublicClient(), nil)
			if err != nil {
				log.Fatalf("connectors: %v", err)
			}
			ls.conn = g
			for name, why := range g.Registry().Problems {
				log.Printf("connectors/%s left out: %s", name, why)
			}
			log.Printf("connectors on: %s", strings.Join(g.Registry().IDs(), ", "))
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
		// the decks' Git repository (gitstore.go, gitpush.go), when git.json
		// turns it on; the settings page turns it on and off later
		ls.gits = &gitService{data: *data, docs: bucket.docs, blobs: bucket.blobs, feed: e.Store, bucket: bucket, client: newPublicClient(), logf: log.Printf}
		if gc, on, err := readGitConfig(*data); err != nil {
			log.Fatalf("data folder %s: %v", *data, err)
		} else if on && gc.Enabled {
			if err := ls.gits.start(stop, gc); err != nil {
				log.Fatalf("git: %v", err)
			}
			kind += ", decks in Git"
			if gc.Remote != "" {
				kind += " (pushed to " + gc.Remote + ")"
			}
		}
		e.agents.ended = ls.endWork
		// the server's own token, said in the log, when it needs one
		if ls.authRequired() {
			ls.secret()
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
		ls.gits.stop()
		return
	}
	var handler http.Handler
	kind := "link"
	{
		u := *base
		if u == "" {
			u = "https://sliqtly.com"
		}
		e := &Env{BaseURL: u, Client: newPublicClient(), GitHubToken: os.Getenv("SLIQTLY_GITHUB_TOKEN"), GitHubUsers: githubUsers(os.Getenv("SLIQTLY_GITHUB_USERS")), Plugins: splitList(os.Getenv("SLIQTLY_PLUGINS"))}
		cloud := os.Getenv("K_SERVICE") != "" || os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" || os.Getenv("FIRESTORE_EMULATOR_HOST") != ""
		if env("SLIQTLY_STORE", "") == "link" {
			cloud = false
		}
		if cloud {
			err := connectFirebase(context.Background(), e, cloudProject(), cloudBucketName())
			if err != nil {
				log.Fatalf("firebase: %v", err)
			}
			kind = "cloud"
			e.Quota = dailyQuota(e.DB, 50, 500, time.Now)
			e.Renders = dailyRenders(e.DB, 100, 500, time.Now)
			// the editor at /editor, for signed-in people (editor.go), when
			// the binary has it built in
			// the taxi game's lines (ttd.go), when the assistant is Gemini
			if m, _ := newAIModel(context.Background()); m != nil {
				e.Taxi = newTTDAI(m)
				// Warden's assistant on the page (wardenai.go)
				if e.warden = newWardenService(e.DB); e.warden != nil {
					e.warden.ai = newWardenAssist(m)
				}
			}
			e.EditorGate = newEditorGate(webFiles(*web), e.DB, e.VerifyIDToken, splitList(os.Getenv("SLIQTLY_ADMIN_EMAILS")))
			if e.EditorGate != nil {
				e.Editor = true
				kind += ", editor at /editor"
				// the accounts' own GitHub repositories (cloudgit.go)
				if c, err := cloudGitFromEnv(e); err != nil {
					log.Printf("GitHub App: %v", err)
				} else if c != nil {
					e.CloudGit = c
					kind += ", GitHub " + c.app.Slug
				}
				if m, what := newAIModel(context.Background()); m != nil {
					e.EditorGate.ai = newAIAssist(m, e.DB)
					kind += ", AI assistant: " + what
				}
			}
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
