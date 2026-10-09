# Plugin: code-review

A code review as a set of views of what the change means, not a list of
changed files: who takes part, which states and transitions there are, how
data moves and where it rests, who waits for whom, and what happens when
something fails. You read the code and write a model of it; Sliqtly checks
the model against the pull request, works out the review order and the
risks, and draws the views as a review deck.

## Steps

1. `sliqtly_plugin(name="code-review", op="start", args={pr})`: the PR's
   facts, its changed files with their patches, and lines in the change
   that look like storage, network calls, waits, failures, states or
   events. These are hints to check, not findings.
2. Read the changed code, and the code around it the change depends on.
3. Write the model (below). Every item has `refs`: the lines it comes from
   at the PR's head. Write only what the code shows; an item no line backs
   is drawn dashed as inferred.
4. `sliqtly_plugin(name="code-review", op="build", args={pr, model})`:
   warnings about the model (fix them and build again), the review order,
   the risks and the deck in Markdown. `args.slice` builds the deck for one
   slice.
5. `create_presentation` with the deck. The views come from the model:
   change the model rather than the diagrams.

`pr` is a pull request's link or `owner/repo#12`.

## The model

A JSON object. Each list is optional; ids are one namespace for all of
them.

```json
{
  "title": "Approval state for reviews",
  "actors": [
    {"id": "user", "label": "Reviewer", "kind": "user", "refs": ["web/review.tsx:12"]},
    {"id": "api", "label": "Review API", "kind": "service", "change": "changed", "refs": ["src/api/review.ts:40-88"]}
  ],
  "stores": [
    {"id": "db", "label": "reviews table", "kind": "db", "durable": true, "fields": ["review_status"], "change": "changed", "refs": ["db/migrations/031.sql:1-9"]}
  ],
  "states": [
    {"id": "draft", "label": "Draft", "of": "review", "refs": ["src/review/states.ts:3"]},
    {"id": "approved", "label": "Approved", "of": "review", "change": "added", "refs": ["src/review/states.ts:6"]}
  ],
  "transitions": [
    {"id": "t1", "from": "draft", "to": "approved", "trigger": "APPROVE", "guard": "role:reviewer", "change": "added", "refs": ["src/review/machine.ts:22"]}
  ],
  "flows": [
    {"id": "f1", "from": "user", "to": "api", "payload": "ApproveRequest", "critical": true, "refs": ["web/review.tsx:40"]},
    {"id": "f2", "from": "api", "to": "db", "payload": "review row", "transform": "validate + map", "critical": true, "refs": ["src/api/review.ts:61"]}
  ],
  "events": [
    {"id": "e1", "name": "review.approved", "from": "api", "to": ["mailer"], "refs": ["src/api/review.ts:70"]}
  ],
  "waits": [
    {"id": "w1", "who": "api", "on": "mailer", "what": "send result", "blocking": false, "timeout": "", "refs": ["src/api/review.ts:72"]}
  ],
  "failures": [
    {"id": "x1", "at": "f2", "when": "DB write fails", "then": "return 500, nothing emitted", "path": "error", "refs": ["src/api/review.ts:64"]}
  ],
  "effects": [
    {"id": "s1", "at": "api", "what": "email to the author", "external": true, "refs": ["src/mail.ts:15"]}
  ],
  "slices": [
    {"id": "approve", "label": "Reviewer approves", "items": ["user", "api", "f2", "db", "t1"]}
  ],
  "narrative": {
    "summary": "Adds an Approved state ...",
    "critical_path": ["user", "f1", "api", "f2", "db"],
    "story": [
      {"text": "Reviewers could only reject; approving meant a message to the author."},
      {"link": "therefore", "text": "The API gets an Approved state.", "refs": ["src/review/machine.ts:22"]},
      {"link": "but", "text": "The mail goes out before the row is saved, so a failed save still tells the author it was approved.", "refs": ["src/api/review.ts:61-72"]},
      {"link": "therefore", "text": "Read the save first."}
    ]
  }
}
```

| Field | Values |
|---|---|
| `refs` | `"path:line"`, `"path:a-b"`; paths from the repository root, lines at the PR's head |
| `change` | `added`, `changed`, `removed`, `same` (default) |
| actor `kind` | `user`, `service`, `worker`, `scheduler`, `queue`, `external`, `db` |
| store `kind` | `db`, `file`, `cache`, `queue`, `blob`, `memory`; `durable` true when it outlives a restart |
| transition | `from`/`to` are states; `trigger` the event (none: taken at once); `guard` a name |
| flow | `from`/`to` are actors or stores; `payload` the data's shape; `transform` what happens to it on the way; `critical` |
| wait | `who` waits `on` whom for `what`; `blocking`; `timeout` ("" when there is none) |
| failure | `at` an item; `when`, `then`; `path`: `error`, `retry`, `compensation`, `normal` |
| state `of` | the thing the state belongs to: one statechart per `of` |

Items that did not change but explain the change (the store a new flow
writes to, the state a new transition leaves) belong in the model with
`change` left out; they are drawn as context.

## The story

`narrative.story` tells the change as a story, beat by beat, on the
deck's second slide. Each beat after the first is joined to the one
before by **but** (what gets in the way: a failure, a wait, a guard, an
old behaviour to keep) or **therefore** (what follows from it), never by
"and then": a reader keeps reading to see how the tension resolves.
Vary the length of the beats, a short one after long ones and a long one
after short; three of about the same length in a row read as a drone.
`link` takes `but` or `therefore` (also `however`, `so`, `mutta`,
`siksi`, `joten`). Build warns about a beat joined any other way, beats
of one length in a row, and a story with no "but". Without a story, build
drafts one from the model (the critical path as "therefore", its failures
and waits without a timeout as "but"); rewrite it as the change's own.

## What build gives

- **Review order**: entry point, critical path, state changes,
  persistence, side effects, failure handling; each step with the places
  to read, each file once.
- **Risks**, each with its reason and line: a write to a store with no
  validation on the way or no failure path, a wait with no timeout, a
  blocking wait on an outside service, a failure with no `then`, a changed
  transition with no guard, an outside effect on the critical path, a
  changed item no line backs.
- **Views** that have something in them: Overview, States (one per `of`),
  Data flow, When something fails, Who waits for whom, Where data rests.
  Changed items have a thick border, removed and inferred ones a dashed
  one. Each view's speaker notes list its items with links to their lines.
