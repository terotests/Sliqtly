# Connectors (server of one's own, trial)

Connectors let scripts and workflows inside a presentation use services
outside Sliqtly (GitHub, Jira, …) through the server. A script never gets
the network, an address or a key: it names a connector, an operation and
its arguments, and the server checks, calls and cuts the answer down.
Design: `workflow/CONNECTORS.md` in the project files.

This first version is the server side: the connector files, the admin's
grants, each person's own sign-in (OAuth), the call endpoint and the
settings page. Scripts in presentations (` ```app ` blocks, XState actions)
will call it once those exist.

## Turn it on

Off by default. Start the server with

```
sliqtly-server -data ~/Sliqtly -connectors
```

or `SLIQTLY_CONNECTORS=1`. Connectors are files in `<data>/connectors/`:
one `<id>.json` per service. The server reads them when it starts; a file
that does not hold is left out and named in the log and on the settings
page.

| File | What |
|---|---|
| `connectors/<id>.json` | a connector, written by the admin |
| `connectors/grants.json`, `requests.json` | the grants, and the calls still waiting for one |
| `connectors/key` | the key the tokens are encrypted with (made on first start, 0600) |
| `connectors/tokens/<id>/…` | each person's sign-in, encrypted |
| `connectors/audit.log` | one line per call: who, presentation, operation, result; no arguments or answers |

## Try it with GitHub

1. On GitHub: Settings → Developer settings → OAuth Apps → New OAuth App.
   - Homepage URL: the server's address, e.g. `http://localhost:8080`
   - Authorization callback URL: `http://localhost:8080/connectors/oauth/callback`
     (the settings page shows the exact address; open the server at that
     same address when you connect)
   - Make a client secret.
2. Put the connector in `<data>/connectors/github.json`:

```json
{
  "id": "github",
  "title": "GitHub",
  "baseUrl": "https://api.github.com",
  "egress": ["api.github.com"],
  "headers": { "X-GitHub-Api-Version": "2022-11-28" },
  "oauth": {
    "provider": "github",
    "clientId": "<the app's client id>",
    "clientSecret": { "env": "SLIQTLY_GITHUB_OAUTH_SECRET" },
    "scopes": ["read:user"]
  },
  "operations": {
    "getWorkflowRuns": {
      "title": "Latest workflow runs",
      "path": "/repos/{owner}/{repo}/actions/runs",
      "in": { "type": "object", "required": ["owner", "repo"], "properties": {
        "owner": { "type": "string", "pattern": "^[A-Za-z0-9-]+$" },
        "repo": { "type": "string", "pattern": "^[A-Za-z0-9._-]+$" },
        "per_page": { "type": "integer", "minimum": 1, "maximum": 30 }
      } },
      "pick": ["total_count", "workflow_runs[].name", "workflow_runs[].conclusion",
               "workflow_runs[].status", "workflow_runs[].head_branch", "workflow_runs[].created_at"]
    },
    "listPulls": {
      "title": "Open pull requests",
      "path": "/repos/{owner}/{repo}/pulls",
      "in": { "type": "object", "required": ["owner", "repo"], "properties": {
        "owner": { "type": "string", "pattern": "^[A-Za-z0-9-]+$" },
        "repo": { "type": "string", "pattern": "^[A-Za-z0-9._-]+$" },
        "state": { "type": "string", "enum": ["open", "closed", "all"] }
      } },
      "pick": ["number", "title", "user.login", "draft", "updated_at"]
    }
  },
  "limits": { "perMinute": 30 }
}
```

3. Start the server with the secret in its environment:

```
SLIQTLY_GITHUB_OAUTH_SECRET=<secret> sliqtly-server -data ~/Sliqtly -connectors
```

4. Open `/settings` on the server's own computer → Connectors → Connect
   GitHub. GitHub asks you to authorize the app and sends you back.
5. Try: pick `getWorkflowRuns`, arguments `{"owner": "terotests", "repo": "sliqtly"}`.

`read:user` reads public repositories only. Private repositories need the
`repo` scope, which on an OAuth App also allows writing; a GitHub App with
read-only permissions is the narrower choice later.

## A connector file

| Field | |
|---|---|
| `id` | lower-case letters, digits and `-`; the file is `<id>.json` |
| `baseUrl` | `https://…`; every operation's path goes under it |
| `egress` | the hosts calls and redirects may reach (default: baseUrl's host) |
| `identity` | `user` (each person's own OAuth sign-in) or `service` (the connector's secret); `user` when `oauth` is set |
| `oauth` | `provider: "github"`, or `authorizeUrl` + `tokenUrl`; `clientId`; `clientSecret: {"env": "NAME"}`; `scopes` |
| `auth` | for `service`: `{"kind": "bearer" or "header", "header": "X-Api-Key", "secret": {"env": "NAME"}}` |
| `headers` | fixed headers (not Authorization, Cookie or Host) |
| `operations.<name>` | `method` (GET default), `path` with `{param}`s, `in` (the arguments: a JSON Schema object with `properties`, `required`, and per property `type`, `pattern`, `enum`, `maxLength`, `minimum`, `maximum`), `pick` (the fields handed back), `effect` (`read`, `write`, `external-write`) |
| `limits` | `perMinute` (30), `perDay` (2000), `maxResponseBytes` (256 KB, at most 4 MB), `timeoutMs` (8000, at most 30000) |

Secrets are never in the file: only the name of the environment variable
that holds them. Arguments not in `in` are refused; `{param}`s are escaped
into their path segment; the rest go in the query of a GET or DELETE and in
a JSON body otherwise (or where the property's `in` says). An error from the
service is passed on as its status only.

## Who approves

The admin approves every grant: the server's own user on the server's own
computer, or a signed-in account named in `-admin` / `SLIQTLY_ADMINS`
(emails, comma separated). A call from a presentation without a grant is
refused with `forbidden` and waits on the settings page under "Waiting for
your approval".

## API

Under `/api/v1` with the same sign-in as the rest of it (`docs/api-v1.md`).
The settings page uses the same calls under `/api/settings/connectors`, as
the server's own user.

```
GET    /api/v1/connectors
  { "connectors": [ { "id", "title", "identity", "oauth", "operations": [ {name, title, effect, in} ],
                      "connected"?, "account"? } ],
    "admin": bool, "problems"?: {file: why}, "callback"?: "<url>" }

POST   /api/v1/connectors/{id}/connect      → { "url": "<the service's sign-in>" }
DELETE /api/v1/connectors/{id}/connection   → 204
GET    /connectors/oauth/callback           (the service sends the browser here)

POST   /api/v1/connectors/call { "deck", "connector", "op", "args" }
  → 200 { "result": <the picked answer> }
  → { "error", "code" } with code
      forbidden 403       no grant yet (the admin is asked)
      not_connected 409   connect your account first (or again)
      bad_request 400     arguments do not fit
      not_found 404       no such connector, operation or deck
      not_configured 503  the server lacks the secret
      quota 429, timeout 504, too_large 502, remote 502 (+ "status")

GET    /api/v1/connectors/grants            admin: { grants, requests, recent }
POST   /api/v1/connectors/grants { "deck" (id or "*"), "connector", "ops": [...] }   admin
DELETE /api/v1/connectors/grants { "deck", "connector", "ops": [] }  admin: revoke ([] = all)
DELETE /api/v1/connectors/grants { "deck", "connector", "op" }       admin: dismiss a request
```
