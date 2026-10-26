# GitHub sign-in and a repository of one's own

The editor at sliqtly.com/editor can sign in with GitHub beside Google, and
can keep an account's presentations as Markdown in one private GitHub
repository of the user's choosing (`mcp-go/cloudgit.go`). Both are off until a
GitHub App is registered and its settings are given to the service.

The repository has the same layout as a server of one's own keeps in its Git
folder (`mcp-go/gitstore.go`):

```
decks/<id>/deck.md      the presentation's Markdown
decks/<id>/deck.json    name, theme, room, folder, its files
decks/<id>/theme.css    its own CSS, when it has one
decks/<id>/<file>       pictures and data files (up to 50 MB each)
rooms/<id>.json         topics (rooms) and their folders
README.md, sliqtly.json, .gitattributes
```

## What happens

- **Sign-in** (`/editor/github/login?mode=in`): GitHub's user authorization,
  then a Firebase custom token for the account the GitHub user is linked to
  (or a new `gh-<id>` account). The GitHub token is not stored.
- **Connecting a repository** (`/editor/git`, File → Git and GitHub…): the
  user links GitHub to their account, then picks one private repository the
  App is installed on, or creates a new one (`sliqtly-decks` by default).
  A public repository is refused.
- **Backups**: File → Save makes a commit with the given message; otherwise a
  commit follows 5 minutes after the last change, and when the page is hidden.
  Only changed files are sent.
- **Changes made on GitHub** come in before each commit, and at once by the
  push webhook: Markdown and CSS are merged three-way with the editor's
  changes. When both changed the same lines, Sliqtly's text is kept and the
  conflict is shown on `/editor/git`, where the user takes either side.
- **Version history** in the editor lists the repository's commits.

A public repository is used only when the person says so when picking it
(a confirmation on the page); one made public on GitHub later stops the
syncs until the person allows it on the page.

What is never done: the branch is only moved forward (no force push); a deck
removed on GitHub is not removed from Sliqtly; only decks this account wrote
are removed from the repository; other paths in the repository are left as
they are.

## A topic in a repository, with branches

A topic of one's own can be kept in a repository the App may write, or in
one folder of it (`mcp-go/cloudtopicgit.go`, the page `/editor/git/topic?room=<id>`,
opened from the topic's **GitHub…** button in its Stories view). Apart from
the account's repository above, and readable on GitHub as it is:

```
<folder>/<slug>.md         a presentation's Markdown
<folder>/<slug>/deck.json  its Sliqtly id, name, theme, its files
<folder>/<slug>/theme.css  its own CSS, when it has one
<folder>/<slug>/<file>     its pictures and data files
```

Markdown files already in the folder become presentations of the topic.
Main syncs as the account's repository does (Save, quiet minutes, webhook).

A branch (`sliqtly/<name>` on GitHub, the topic's folder "⎇ name" in
Sliqtly) starts at main. "Edit in the branch" makes a copy of a
presentation for the branch; a presentation put in the branch's folder is
new in it. "Update from main" merges main's changes into the copies
(diff3); clashes are chosen from on the page, one place at a time. "Merge
into main" updates first, gives the branch a merge commit with main, then
opens a pull request and merges it. When GitHub requires reviews or checks
the pull request waits there, and the push to main finishes the merge in
Sliqtly. "Drop" deletes the copies and keeps the GitHub branch.

## Registering the App

GitHub → Settings → Developer settings → GitHub Apps → New GitHub App:

| Setting | Value |
|---|---|
| Homepage URL | `https://sliqtly.com` |
| Callback URL | `https://sliqtly.com/editor/github/callback` |
| Request user authorization (OAuth) during installation | off (GitHub then greys out the setup URL) |
| Setup URL | `https://sliqtly.com/editor/github/setup` |
| Redirect on update | on |
| Webhook URL | `https://sliqtly.com/editor/github/hook` |
| Webhook secret | a long random string (`SLIQTLY_GITHUB_HOOK_SECRET`) |
| Where can this App be installed | Any account |

Repository permissions:

- Contents: Read and write
- Metadata: Read-only
- Pull requests: Read and write (a topic's branch is merged through a pull
  request; without it main is moved forward to the branch instead)
- Administration: not needed, and best left at No access. With Read and
  write, "Make it" creates the repository from Sliqtly; without it the page
  sends the user to create it on GitHub and pick it from the list.

Account permissions:

- Email addresses: Read-only (the verified address of a new account)

Subscribe to events: **Push**. Installation events are sent without a
subscription.

Then generate a client secret and a private key (.pem).

## Settings of the service

GitHub repository variables (Settings → Secrets and variables → Actions):

| Variable | Value |
|---|---|
| `SLIQTLY_GITHUB_APP_ID` | the App ID |
| `SLIQTLY_GITHUB_APP_CLIENT_ID` | the Client ID (`Iv…`) |
| `SLIQTLY_GITHUB_APP_SLUG` | the App's name in its URL, `github.com/apps/<slug>` |

Secrets:

| Secret | Value |
|---|---|
| `SLIQTLY_GITHUB_APP_CLIENT_SECRET` | the client secret |
| `SLIQTLY_GITHUB_APP_KEY` | the private key: `base64 -w0 app.private-key.pem` |
| `SLIQTLY_GITHUB_HOOK_SECRET` | the webhook secret |
| `SLIQTLY_GIT_SWEEP_KEY` | optional, a random string for the sweep below |

`deploy-mcp-go.yml` passes them to the service when `SLIQTLY_GITHUB_APP_ID`
is set, and lets the service's account sign Firebase custom tokens
(Service Account Token Creator on itself). `SLIQTLY_GITHUB_SIGNIN=off`
keeps repositories but hides the GitHub sign-in button.

## Optional: a nightly sweep

A missed webhook or a closed page leaves changes until the next save. A
Cloud Scheduler job can sync every connected account:

```
gcloud scheduler jobs create http sliqtly-git-sweep --schedule "0 3 * * *" \
  --uri https://sliqtly.com/editor/github/sweep --http-method POST \
  --headers X-Sliqtly-Key=<SLIQTLY_GIT_SWEEP_KEY>
```

## Data in Firestore

`git_links/{uid}` holds an account's repository, its last commit and its
conflicts; `git_users/{github id}` which account a GitHub user is. No rule
matches them: only the service reads or writes them.
