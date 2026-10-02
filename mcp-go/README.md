# Sliqtly MCP server in Go (prototype)

A Go port of [`mcp/`](../mcp) for Cloud Run: one static binary in a
distroless container. Same tools, same Firestore documents and Storage paths,
same OAuth endpoints and the same `guide.md` / `preview.html` (copied from
`mcp/` at build time), so either server can answer for the other and a
sign-in started on one can finish on the other.

Not deployed. `mcp/` (the Cloud Function) is still what `sliqtly.com/mcp`
runs.

## Why hand-written Go and not Ranger

The current server is plain Node.js (Express, the MCP TypeScript SDK,
firebase-admin, zod); no Ranger code runs in it.

Ranger's Go target cannot carry this server yet. `lib/WebServerLib.rgr` on Go
offers routes, query variables, the request body, a content type and a text
response, but no request headers (`Authorization`, `Accept`,
`X-Forwarded-*`), no status codes or redirects, and no CORS. `Ajax.rgr` and
`Crypto.rgr` have no Go templates (no outbound HTTP, no SHA-256), and there is
no Firestore, Cloud Storage, Firebase Auth or MCP protocol library for Ranger
at all. Writing those as Ranger system classes would be most of the work and
would wrap the same Go libraries used here.

So the server is Go, with Google's own clients (`cloud.google.com/go/firestore`,
`cloud.google.com/go/storage`, `firebase.google.com/go/v4` for ID tokens) and
the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`, stateless
Streamable HTTP with JSON responses, as the Node server).

## Measured

Both servers in containers on this machine, `--cpus 1 --memory 512m`; Node is
`mcp/` with its production dependencies on `node:22-slim`, started with what
`index.js` does (`initializeApp`, Firestore, Storage, Auth, OAuth). Cold start
is `docker run` until the first MCP `initialize` is answered, median of 10.
Throughput: `create_presentation` (link mode, no Firestore), 40 concurrent,
5000 calls.

| | Go | Node.js |
| --- | --- | --- |
| Image (uncompressed / compressed) | **55 MB / 13 MB** | 467 MB / 97 MB on node:22-slim |
| Cloud Functions' own runtime base image | — | 1.8 GB / 450 MB, plus 118 MB `node_modules` |
| Process ready (log line) | **2 ms** | 1.3 s (loading modules) |
| Cold start to first answer | **0.25 s** | 1.7 s |
| Memory, idle after first request | **6 MB** | 58 MB |
| Memory after the load test | **18–21 MB** | 81 MB |
| Throughput on 1 vCPU | **~1000 req/s**, p50 24 ms | ~200 req/s, p50 172 ms |

On Cloud Run the platform adds its own start-up (scheduling, image fetch) to
both, so the real difference in cold start is about the 1.3 s Node spends
loading `firebase-admin`, the MCP SDK and Express before it can answer. The
memory and CPU figures say one Go instance at 256 MiB handles what the
function now gets 512 MiB and 40-request concurrency for.

Firestore and Storage calls are network-bound and take the same time from
either language; they were not part of the load test.

## Differences from the Node server

- One MCP server per instance instead of one per request; who is asking (the
  signed-in user, the rate-limit key) comes with the request's context. Building
  the five tool definitions per request cost more than the request itself.
- Theme sheets are cached for the life of the instance (Node fetches them on
  every call that has `css`).
- Pictures and client metadata documents are fetched with a client whose
  dialer refuses private, loopback and link-local addresses, so a public name
  that resolves to `10.x` or `169.254.169.254` is refused too. Node checks only
  the name.
- Bad arguments get the same kind of error result, with Go's wording instead of
  zod's.

## Run and test

```
cd mcp-go
go generate         # copies guide.md and preview.html from ../mcp into assets/
go test ./...       # mcp/test/server.test.js, case for case, Firestore/Storage faked
go run .            # http://localhost:8080/mcp, decks travel in the link
```

With `GOOGLE_APPLICATION_CREDENTIALS` (or on Cloud Run, `K_SERVICE` set) it
writes real shares and offers sign-in. `SLIQTLY_URL`, `SLIQTLY_BUCKET`,
`GOOGLE_CLOUD_PROJECT` and `PORT` work as for `mcp/`; `SLIQTLY_STORE=link`
forces the link-only mode.

## Plan: from prototype to `sliqtly.com/mcp`

Each step needs Tero's go-ahead; nothing here has been run.

1. **Deploy beside the function**, no traffic:
   ```
   IMAGE=europe-west1-docker.pkg.dev/sliqtly/mcp/sliqtly-mcp-go
   docker build -f mcp-go/Dockerfile -t $IMAGE .     # from the repository root
   docker push $IMAGE
   gcloud run deploy sliqtly-mcp --image $IMAGE \
     --region europe-west1 --project sliqtly --allow-unauthenticated \
     --cpu 1 --memory 256Mi --concurrency 80 --max-instances 10 --cpu-boost
   ```
   The service account needs
   Cloud Datastore User and Storage Object Creator on the `sliqtly` project;
   verifying Google ID tokens needs no role.
2. **Test it on its `run.app` URL** with the MCP Inspector and Claude: create,
   update, read, list, sign-in. The decks it writes are real shares.
3. **Switch the rewrites** in `firebase.json` from the function to the service
   (`"run": { "serviceId": "sliqtly-mcp", "region": "europe-west1" }` for
   `/mcp`, `/oauth/**` and the `/.well-known` documents) and deploy Hosting.
   Rolling back is the same edit the other way.
4. **Retire the function** once the service has carried traffic for a while:
   remove `mcp/index.js`'s export and the Deploy MCP workflow, keep `mcp/`'s
   guide and preview (or move them here).
