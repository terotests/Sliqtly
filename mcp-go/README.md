# Sliqtly MCP server in Ranger, compiled to Go (prototype)

The MCP server of [`mcp/`](../mcp) written in Ranger and compiled to Go for
Cloud Run: one static binary in a distroless container. Same tools, same
Firestore documents and Storage paths, same OAuth endpoints and the same
`guide.md` / `preview.html` (copied from `mcp/` at build time), so either
server can answer for the other and a sign-in started on one can finish on
the other.

Not deployed. `mcp/` (the Cloud Function, Node.js) is still what
`sliqtly.com/mcp` runs.

## What is Ranger and what is Go

| | |
| --- | --- |
| [`rgr/App.rgr`](rgr/App.rgr) | routing, CORS, the `/mcp` transport checks, sign-in on a request |
| [`rgr/Mcp.rgr`](rgr/Mcp.rgr) | MCP: JSON-RPC, `initialize`, `tools/*`, `resources/*` |
| [`rgr/Tools.rgr`](rgr/Tools.rgr) | the five tools, their schemas and UI metadata, the preview resource |
| [`rgr/OAuth.rgr`](rgr/OAuth.rgr) | the OAuth 2.1 server: registration, authorize, approve, token, refresh |
| [`rgr/Store.rgr`](rgr/Store.rgr) | shares, edit keys, pictures, listing |
| [`rgr/Deck.rgr`](rgr/Deck.rgr) | the checks: picture names and types, outline, warnings |
| [`rgr/Json.rgr`](rgr/Json.rgr) | JSON: Ranger's own `MfJ` (gallery/mfiles) and a writer |
| [`rgr/McpHost.rgr`](rgr/McpHost.rgr) | the operators the Go host implements |
| `sliqtly_mcp.go` | what Ranger compiles `rgr/` to (generated, committed) |
| [`host.go`](host.go), [`net.go`](net.go), [`firebase.go`](firebase.go), [`main.go`](main.go) | the Go around it |

The Ranger code uses what Ranger's Go target already has: `HttpRequest` /
`HttpResponse` and their operators (method, path, headers, status, body out),
strings, lists, and the `MfJ` JSON value and reader from `gallery/mfiles`.
The Go host adds what it does not have, behind the operators in
`McpHost.rgr`: Firestore, Cloud Storage and Firebase Auth (Google's Go
clients), outbound HTTP to public addresses, SHA-256, random ids, deflate,
URL parsing, the request body, and the state that lasts between requests
(rate limiter, theme cache). Values cross as strings; documents as JSON.

Two things found on the way, worked around here and worth fixing in Ranger:

- `MfJ.emit` appends to a list passed as a parameter; on Go the list is a
  slice passed by value, so `toJson()` returns an empty string. `JOut` in
  `Json.rgr` keeps its list in a field instead.
- `sha256` in `Lang.rgr` has no Go template, and `http_get_body` from the
  HTTP plan is not in `Lang.rgr`. Both are host operators here.

## Measured

Both servers in containers on this machine, `--cpus 1 --memory 512m`; Node is
`mcp/` with its production dependencies on `node:22-slim`, started with what
`index.js` does. Cold start is `docker run` until the first MCP `initialize`
is answered, median of 10. Throughput: `create_presentation` (link mode, no
Firestore), 40 concurrent, 5000 calls.

| | Ranger → Go | Node.js |
| --- | --- | --- |
| Image (uncompressed / compressed) | **53 MB / 12 MB** | 467 MB / 97 MB on node:22-slim |
| Cloud Functions' own runtime base image | — | 1.8 GB / 450 MB, plus 118 MB `node_modules` |
| Process ready (log line) | **under 1 ms** | 1.3 s (loading modules) |
| Cold start to first answer | **0.24 s** | 1.7 s |
| Memory, idle after first request | **5 MB** | 58 MB |
| Memory after the load test | **17–18 MB** | 81 MB |
| Throughput on 1 vCPU | **1600–2100 req/s**, p50 11–16 ms | ~200 req/s, p50 172 ms |

On Cloud Run the platform adds its own start-up to both. Firestore and Storage
calls take the same time from either language and were not part of the load
test.

## Run and test

```
cd mcp-go
go generate         # copies guide.md and preview.html from ../mcp, compiles rgr/ (needs node)
go test ./...       # mcp/test/server.test.js case for case, over HTTP with the official MCP Go client
go run .            # http://localhost:8080/mcp, decks travel in the link
```

`go generate` compiles with the Ranger checkout the editor builds with
(`npm run setup`, `.deps/Ranger` at the ref in `presentation.config.json`), or
`RANGER_DIR`. Building the binary or the container needs only Go:
`sliqtly_mcp.go` is committed, and CI checks it is what `rgr/` compiles to.

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
   The service account needs Cloud Datastore User and Storage Object Creator
   on the `sliqtly` project; verifying Google ID tokens needs no role.
2. **Test it on its `run.app` URL** with the MCP Inspector and Claude: create,
   update, read, list, sign-in. The decks it writes are real shares.
3. **Switch the rewrites** in `firebase.json` from the function to the service
   (`"run": { "serviceId": "sliqtly-mcp", "region": "europe-west1" }` for
   `/mcp`, `/oauth/**` and the `/.well-known` documents) and deploy Hosting.
   Rolling back is the same edit the other way.
4. **Retire the function** once the service has carried traffic for a while.
