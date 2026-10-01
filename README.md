# Sliqtly

Deploys the Sliqtly editor to Firebase Hosting. The editor itself lives in
[terotests/EVGPresentation](https://github.com/terotests/EVGPresentation);
this repository says which version of it is published and where.

- `sliqtly.config.json`: the repository and ref (branch, tag or commit) that is built.
- `.firebaserc`: the Firebase project, `sliqtly`.
- `firebase.json`: Hosting serves the build output, `app/web/dist`.
- `.github/workflows/deploy.yml`: on a push to `main`, or run by hand
  (Actions → Deploy → Run workflow, optionally with another ref), it checks
  out the editor into `app/`, builds and checks it, and deploys it live.

A change in EVGPresentation is not deployed by itself: run the workflow, or
pin a new commit in `sliqtly.config.json` and push.

## One-time setup

1. Create a Firebase project and enable Hosting.
2. In Google Cloud Console, create a service account in that project with
   the role **Firebase Hosting Admin** and download a JSON key.
3. In this repository, Settings → Secrets and variables → Actions:
   - secret `FIREBASE_SERVICE_ACCOUNT`: the whole JSON key

## Domain (Cloudflare)

1. Firebase Console → Hosting → Add custom domain → `sliqtly.com`
   (and `www.sliqtly.com`, redirected to it).
2. Add the TXT and A records Firebase shows in Cloudflare → DNS, with the
   proxy status **DNS only** (grey cloud). Behind Cloudflare's proxy,
   Firebase cannot verify the domain or issue its certificate.
3. Wait until Firebase shows the domain as connected (minutes to a few hours).
