# ADR 0001: The Room is Sliqtly's top-level concept

Status: accepted (2026-10-05, Tero). Decides what the storage schema is
built around. The store contract (`mcp-go/store`) implements it: `Rooms`,
`RoomPolicy`, `Links`, `LinkTypes`.

## The question

What is the unit everything else belongs to: the Document (a deck), a
Ticket/Container (Jira-shaped: epic, story, folder), or a Room?

## Decision

**Room.** A room is Sliqtly's own collaboration context: who works
together, on which documents and files. It bounds a subject, for example
one task; it often matches one ticket. A room holds many presentations, and
a presentation has its own files. A deck lives in exactly one room. A
Jira issue, a GitHub issue or a Confluence page is an external object that
a room *refers to*; it does not decide the room's shape. Jira's hierarchy
(epic → story) is metadata of the external system, or a `parent` room link,
never a structure Sliqtly enforces.

The room is the boundary for:

| boundary | what it means |
| --- | --- |
| collaboration | who is in the same workspace |
| authorization | ACL (and PostgreSQL RLS) is written around `room_id` |
| storage namespace | documents and files belong to a room |
| change feed | Watch / `/api/events` can be scoped to a room |
| integration | Jira / GitHub / Confluence refs belong to a room |
| search scope | "search this room", "and its linked rooms" |
| export / import | one room packs into one bundle |

## The room stays small

A room is namespace + identity + policy boundary. It is not a document
that holds everything. Resources are their own rows and point at it:

```
ROOM            id (internal, stable), tenant, title, kind (free text), created
MEMBERSHIP      room_id, principal (user or group), role (viewer | editor | owner)
DOCUMENT        id, room_id, kind (deck | notes | report …), inherit_room_files
FILE_REF        id, room_id, document_id (null: the room's own), path, blob
BLOB            sha256, size, type                      (content, no access)
ROOM_LINK       id, from_room, to_room, kind
EXT_REF         id, room_id, system, key, url           (jira-cloud / ABC-123)
COMMENT, COLLAB_OP, …   room_id + their own fields
```

Every content table carries `room_id`, so one policy covers them all:

```sql
USING (EXISTS (SELECT 1 FROM room_members m
               WHERE m.room_id = documents.room_id
                 AND m.user_id = current_setting('app.user_id')::uuid))
```

On the folder and SQLite the same rule is the application's `Policy`
(`store.Store`); PostgreSQL enforces it a second time with RLS.

## Room links are explicit, and are not permissions

```
ROOM_LINK  from_room  to_room  kind
```

Link kinds are a configurable list (`LinkTypes`), with built-in ones used
from the start: `relates_to` (symmetric), `parent` / `child` (one pair:
"A child of B" is kept as "B parent of A"), `references`,
`inherits_files`. A link is kept in one direction only, so a pair's two
names and a symmetric link's two orders are one link with one id; a kind
not configured is refused.

A link never grants access. "A references B" does not let A's members read
B. A link is shown to a principal only when they can read both rooms. A
link's id is made of its ends and kind as kept (`store.LinkID`). So the same link exists once, in a folder as in SQL, where the
id is the primary key and `(tenant, from, kind, to)` is also unique.

No general "room contains room" nesting for now: hierarchy is `parent`
links, any depth, nothing required, cycles refused when a link is added.

## Files: per document, opt-in inheritance

A document does not automatically see its room's files: sharing is off
unless the document turns it on (`inherit_room_files`, default no).

```
Room files: logo.svg, metrics.xlsx
Deck A   inherit_room_files = yes
Deck B   inherit_room_files = no; own: private-draft.csv
```

Resolving `logo.svg` for a document:

1. the document's own files
2. the room's files, if `inherit_room_files`
3. rooms linked `inherits_files` from the room, in link order
4. an ACL check on every candidate; the first one the principal may read wins

A file is reached only through a FILE_REF whose room the principal may
read. A blob's hash is never a way in: knowing it gives nothing. One file
in two rooms is one blob, two refs. An edit writes a new blob and moves
the one ref, so other rooms do not change underneath their users.

## Identity

Principal = Sliqtly's own user id, tenant, groups, roles. An OIDC
`(provider, subject)` maps to that user id in IDENTITY; an e-mail address
is never a key. A room's external refs work the same way: the room id
stays when `ABC-123` becomes `PAY-817`, a second issue is another
EXT_REF, and one epic can refer to many rooms.

## Migration

1. The store contract and tests (done: `mcp-go/store`).
2. Folder server, format 3: every user gets a room "Omat"/"Mine". Every
   existing deck moves into it (its home room) with
   `inherit_room_files = no`, its files staying its own. URLs (`/s/{id}`, `/files/shares/{id}/…`) do not
   change. The existing format migration (hard-link backup, conflicts set
   aside) does the move.
3. SQLite: `store.Copy` + `store.Verify` (revisions kept, digests
   compared); the folder is left as it was, so going back is starting on
   it.
4. PostgreSQL + RLS: important, right after SQLite; the cloud (Firestore
   collections and rules on `room_id`) after that.

## Consequences

- One policy shape for every resource instead of one per type.
- Document and Jira object both fit under or beside a room; neither
  dictates the system.
- The store's `Containers` layer becomes `Rooms`. `store.OwnerPolicy` is
  the test policy; the product policy is a membership policy on `room_id`.
- Risk: the room becoming a do-everything object. Kept in check by the
  rule above: resources are rows pointing at `room_id`, not fields of the
  room.

## Decided with it (2026-10-05)

- A room often matches a ticket, but it is the subject boundary, not the
  ticket: a room holds several presentations, each with its files.
- A document has one home room; it shows in others through links.
- Room files are not shared by default (`inherit_room_files = no`).
- Link kinds: a configurable list with built-ins (relates_to, parent /
  child, references, inherits_files).
- A room is archived, not deleted: read only for everyone, its owners
  too, until taken out of the archive; nothing in it is removed, and it
  is not listed unless asked for.
- Order: folder and SQLite first; PostgreSQL is important and next.
