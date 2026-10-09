# Operations: the hosted sliqtly.com

How the hosted service is set up: Firebase Hosting, sign-in, the share
store and the domain. Only needed to run or redeploy sliqtly.com; building
and running Sliqtly locally is in the [README](../README.md). The MCP
server's Cloud Run deploy is in [mcp-go/README.md](../mcp-go/README.md).

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
