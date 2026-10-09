# Operations: the hosted sliqtly.com

How the hosted service is set up: Firebase Hosting, sign-in, the share
store, the domain, the MCP server on Cloud Run, the editor's licenses and
the owner's dashboard. Only needed to run or redeploy sliqtly.com; building
and running Sliqtly locally is in the [README](../README.md). The MCP
server's own build and options are in [mcp-go/README.md](../mcp-go/README.md).

## Deploy (Firebase Hosting)

The project is `sliqtly` (`.firebaserc`); `firebase.json` serves `web/dist`.
The Deploy workflow needs one secret, `FIREBASE_SERVICE_ACCOUNT`: the JSON key
of a service account with the role **Firebase Hosting Admin**.

## Google sign-in (PRO)

`web/sliqtly.js` loads Firebase from Google's CDN and the project's config from
Hosting's reserved `/__/firebase/init.js`, so it works only when served by
Firebase Hosting. In the Firebase Console, once:

1. Project settings → General → Your apps → add a **Web app**.
2. Authentication → Sign-in method → enable **Google**.
3. Authentication → Settings → Authorized domains: add `sliqtly.com` when the
   domain is connected (`sliqtly.web.app` is there already).
4. Google Cloud Console → APIs & Services → Credentials → the **Web client
   (auto created by Google Service)** → Authorized redirect URIs: add
   `https://sliqtly.com/__/auth/handler` and
   `https://sliqtly.web.app/__/auth/handler`. When a phone's browser blocks
   Google's sign-in window (or Sliqtly runs from the home screen), sign-in
   goes by redirect through the site's own handler, since Safari keeps no
   storage for `sliqtly.firebaseapp.com` inside `sliqtly.com`.

## Sharing (PRO)

Signed in, the deck is kept as its owner's in Firestore (`decks/{deckId}`)
with a copy under a short random id (`shares/{id}`, its pictures and data
files in Storage under `shares/{id}/`). The copy's `visibility` says who
reads it: `private` only its owner's Google account, `link` anyone with
`/s/{id}`. sliqtly.com is an open demo, so a deck starts `link` (not for private
data: every slide is reachable by its hidden link); `update_presentation` with `visibility: "private"` keeps one for its
owner, who opens it at `/s/{id}` by signing in with Google on the page. A share without the field (made
before it existed, or by an assistant without sign-in) is `link`. The id
locates a deck and grants nothing: an assistant without sign-in changes its
deck only within the MCP session that made it (`mcp_sessions`, keyed by the
hash of the session id the server hands out in `Mcp-Session-Id`, gone when
the session ends or after a day unused). Nobody can
list the ids, only the owner can change or delete the copy
(`firestore.rules`, `storage.rules`; the MCP read tools check the same
field). `/s/{id}?edit` opens the copy as a new deck of the reader's own.
Signed out, Share still packs the text into the link as before.

Firebase Console, once:
1. Firestore Database → Create database (production mode).
2. Storage → Get started (needs the Blaze plan). When the Storage rules are
   first deployed, allow them to read Firestore if the console asks.
3. The Deploy workflow then deploys both rule files (its last step). The
   service account needs the role **Firebase Rules Admin** (the Admin SDK
   account generated in Project settings → Service accounts has it).
4. The bucket's CORS (`storage.cors.json`): a shared deck fetches its
   pictures and data files from Storage, and without it the browser refuses
   them (the slides open without their background images). Any origin may
   read them, since the preview in an AI assistant runs the viewer on the
   assistant's own domain (mcp-go/assets/preview.html). The Deploy
   workflow sets it when its service account has the role **Storage Admin**;
   by hand, in Cloud Shell:
   `gcloud storage buckets update gs://sliqtly.firebasestorage.app --cors-file=storage.cors.json`

## Domain (Cloudflare)

1. Firebase Console → Hosting → Add custom domain → `sliqtly.com`.
2. Add the TXT and A records Firebase shows in Cloudflare → DNS, proxy status
   **DNS only** (grey cloud), or Firebase cannot verify the domain or issue
   its certificate.

## The MCP server on Cloud Run

Actions → **Deploy MCP (Go)** (`.github/workflows/deploy-mcp-go.yml`) runs
the tests, builds the image, deploys the service and checks that it answers
(initialize, the sixteen tools, the OAuth metadata, `/api/hit`):
```
IMAGE=europe-west1-docker.pkg.dev/sliqtly/mcp/sliqtly-mcp-go
docker build -f mcp-go/Dockerfile -t $IMAGE .     # from the repository root
docker push $IMAGE
gcloud run deploy sliqtly-mcp --image $IMAGE \
  --region europe-west1 --project sliqtly --allow-unauthenticated \
  --cpu 1 --memory 512Mi --concurrency 80 --max-instances 10 --cpu-boost
```
Generating the Go code takes about 3.6 GB of memory (Node's heap is raised
to 6 GB in `gen.mjs`), so the machine that builds the image needs more
than 4 GB. The service runs as the project's default compute account, which
needs Cloud Datastore User and Storage Object Admin on `sliqtly`; verifying
Google ID tokens needs no role. The page's own Deploy (Hosting) needs the
service to exist, since its rewrites point at it.

## The editor at sliqtly.com/editor

The public site is the viewer; the editor (web/dist, built into the image)
comes only from this service, at `/editor`, and only to a request carrying a
Google sign-in (editor.go). Anyone else gets a sign-in page with none of the
editor's code. Sign-in is Google's by redirect, no popup; the sign-in is the
`__session` cookie (the one cookie Hosting passes to Cloud Run), holding the
page's Firebase ID token, which the page renews before its hour is up.
Responses are `private`: the CDN keeps none. A presentation in the editor is
at `/editor/d/{id}`; `/s/{id}?edit` and the older `/editor/s/{id}?edit`
redirect there.

Presentations made in the editor are private: only the owner and the people
they invite (`editors` on the share, Google e-mail addresses in lower case)
open and edit them; firestore.rules and storage.rules check the address of a
verified sign-in, and `/editor/api/claim` answers `not-yours` to anyone else
(a deck there is shown read-only, never copied). Share makes a viewing link of
its own, `links/{linkId}` → `{ of, owner }`: `/s/{linkId}`, `/api/view`,
`/api/card` and `/api/export` resolve it to the deck (`Store.shown`), so the
deck's own id is never handed out. `ops/private-editor-decks.mjs` (Actions →
Private editor decks) made the decks from before this private.

What the browser keeps (IndexedDB, rooms, open tabs) is per Google account on
the editor (web/account.js): another account in the same browser sees none of
it. The store from before that is moved to an account only when it says the
decks are its own.

Licenses are `licenses/{uid}` in Firestore, made at the first visit as a
Trial: `plan: "trial"`, `maxDocs: 2`, `docs: []`, with the user's `email`.
The owner changes them in the Firebase console: `maxDocs` (−1 = no limit),
`plan` (a name shown to the user), `editUntil` (a timestamp; after it nothing
is changed in the cloud). The accounts in `SLIQTLY_ADMIN_EMAILS` have no
limit. A presentation is taken under the license (`docs`) when it is first
saved to the cloud, while there is room; firestore.rules lets a page create or
change a share only under its owner's license. Reading, presenting, exporting,
making private and deleting are never limited: a license that ends takes
nothing away. The MCP server writes with the Admin SDK and is not limited by
licenses.

## The owner's dashboard

`sliqtly.com/main/admin` (web/admin.html, linked from nowhere) shows, per UTC
day: visitors and page loads, presentations made (signed in, or by an
assistant without sign-in), new and active signed-in accounts, and the Cloud
bill. The page signs in with Google; the numbers come from
`GET /main/admin/api/stats?days=7|30|90` (admin.go), which answers only a
Firebase ID token whose verified email is in `SLIQTLY_ADMIN_EMAILS`. Without
that variable the route does not exist. The deploy sets it from the
repository variable `SLIQTLY_ADMIN_EMAILS` (Settings → Variables), or the
owner's address when that is unset. A report is kept a minute per instance;
Refresh reads again.

What the service account needs for each part (a part it cannot read says so
on the page, the rest still shows):

- visitors, presentations: Cloud Datastore User (already there)
- accounts: Firebase Authentication Viewer
- the bill: Cloud Billing → Billing export → BigQuery export, "Standard usage
  cost", into a dataset (e.g. `billing_export` in `sliqtly`). Give the
  service account BigQuery Job User on the project and BigQuery Data Viewer
  on that dataset, set the repository variable `SLIQTLY_BILLING_TABLE` to the
  table it makes (`sliqtly.billing_export.gcp_billing_export_v1_XXXXXX_XXXXXX_XXXXXX`),
  and run Deploy MCP (Go). The export fills from the day it is turned on and
  runs about a day behind.
