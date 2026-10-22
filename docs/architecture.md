# Architecture

How Quartzite is put together. For the HTTP endpoints and the viewer protocol,
see [api.md](api.md). For rules when changing the code, see
[AGENTS.md](../AGENTS.md).

Go standard library only. `go.mod` has no `require` block. Requires Go 1.26+
to build. One binary; move the folder anywhere and it still runs.

## The two corpora

| corpus | flag / env | access |
|---|---|---|
| vault | `-vault` / `QUARTZITE_VAULT` — optional | **read-only**, indexed for links and search |
| store | `-store` / `QUARTZITE_STORE` — default `./store` | read-write, everything agents create |

Both corpora are external folders Quartzite opens; neither lives in this repo.
"Vault" is just the conventional name for notes you already have — any folder
of markdown you want in the graph but never written to. An Obsidian vault works
as-is: `.obsidian/`, `.trash/`, `.git/` and other dot-folders are skipped.

A store path that does not exist yet is created and seeded from `seed-store/` —
a fresh store still has the markdown viewer and the Welcome bundle, so the app
works from minute one. No vault is a valid configuration: the server runs with
the store alone.

```
./quartzite -addr :5003 -store ~/quartzite-store -vault ~/notes
QUARTZITE_STORE=~/quartzite-store ./quartzite    # env vars work too; flags win
```

Other flags: `-addr` / `QUARTZITE_ADDR` (default `:5003`), `-web` (the shell's
static files, default `./web`), `-watch` (rescan interval, default `3s`; `0`
disables).

### The vault is read-only

There is no code path that opens a vault file for writing, and the API refuses
to change vault documents: `PATCH` and `DELETE` return 403, `PUT` returns 404.
That is what makes it safe to hand an agent the write API.

Two setups get around it today, because the store's write path does not yet
resolve symlinks or check for overlap:

- `-store` and `-vault` pointing at the same folder, or one nested inside the
  other;
- a symlink inside the store that leads into the vault.

Keep the store a separate folder with no links into the vault. A startup check
for overlapping paths and a write-path check that resolves symlinks are
planned.

## A document is not a file

- **file** — `notes.md`, `chart.html`
- **folder** — an entry point plus assets, served at `/d/{id}/`
- **zip** — the same, read straight out of the archive

Folders and zips both satisfy `fs.FS`, so they share one serving path. Each
bundle gets a URL prefix of its own, which is why relative `<script src>` and
`fetch()` inside a bundle resolve the way its author expected. A directory is a
bundle when it contains `index.html` (or `index.htm` / `main.html` / `app.html`);
otherwise it is just a namespace. A bundle's `meta.json` holds its title, tags,
description and entry point.

Quartzite **serves** bundles, it does not build them. No npm, no bundler —
bundles arrive pre-built and self-contained.

Documents render in a sandboxed iframe (`allow-scripts allow-forms
allow-popups allow-pointer-lock`, no same-origin), so a bundle cannot call the
API directly; it talks to the shell over `postMessage` (see
[api.md](api.md#viewers-and-bundles)).

## IDs and links

Content uses `[[Title]]`. The API uses stable IDs, kept per store in
`.quartzite/ids.json` and keyed by path, so an ID survives a retitle. IDs are
generated when a store is opened, so the same document has different IDs in
different stores.

Renaming rewrites inbound links so references cannot rot, while the ID stays
fixed. Unresolved links are not errors: they render as broken and show in the
dashboard count, because writing toward a document that does not exist yet is
a legitimate way to work.

## Viewers are documents

A document at `viewers/<format>.html` claims that format. It runs sandboxed,
receives its target over `postMessage`, and can render anything. Adding a
format means writing a document — no Go code, no restart. A broken viewer
breaks its own pane and nothing else.

## Daily view

Store documents at `daily/YYYY-MM-DD` (a file or a folder bundle, no
extension in the path) show up in the shell's Daily view at `#/daily`.

## Indexing

A full rescan on a 3s timer plus after every write — ~170ms for 780
documents, which is cheaper than the dependency a file watcher would cost.
Search ranks title > tag > path > body.

## Layout

```
cmd/quartzite/       entry point
internal/model/      Doc, Kind, Source, Stats
internal/store/      read-write corpus; bundles, IDs, create/update/rename
internal/vault/      read-only index of existing notes
internal/graph/      wikilink extraction, resolution, backlinks
internal/parse/      frontmatter, excerpts, code stripping
internal/metrics/    self-observability for the dashboard
internal/api/        HTTP surface, SSE hub, cross-corpus service
web/                 dashboard and reader shell
seed-store/          what a brand-new store is seeded with (viewers, Welcome)
seed.sh              bulk-import a folder of HTML cards into a store
docs/                these docs and the README images
```

The store itself is not here — it is your data, in whatever folder you point
`-store` at.
