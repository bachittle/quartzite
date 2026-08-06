# Quartzite

A local-first knowledge base built for AI agents, on `localhost:5003`.

Plain files, wikilinks, a knowledge graph — but a document does not have to be
markdown, and does not have to be a *file*. Agents write here; you read.

```
go build -o quartzite ./cmd/quartzite
./quartzite                       # localhost:5003, store created at ./store on first run
```

Go standard library only. `go.mod` has no `require` block. Requires Go 1.26+
to build. One binary; move the folder anywhere and it still runs.

## The two corpora

| corpus | flag / env | access |
|---|---|---|
| vault | `-vault` / `QUARTZITE_VAULT` — optional | **read-only**, indexed for links and search |
| store | `-store` / `QUARTZITE_STORE` — default `./store` | read-write, everything agents create |

Both corpora are external folders Quartzite opens; neither lives in this repo.
"Vault" is just the conventional name for notes you already have — any folder
of markdown you want in the graph but never written to. A store path that does
not exist yet is created and seeded from `seed-store/` — a fresh store still
has the markdown viewer and the [[Welcome]] bundle, so the app works from
minute one. No vault is a valid configuration: the server runs with the store
alone.

```
./quartzite -addr :5003 -store ~/quartzite-store -vault ~/notes
QUARTZITE_STORE=~/quartzite-store ./quartzite    # env vars work too; flags win
```

Nothing writes to the vault. That is not a config flag — there is no code path
that opens a vault file for writing. It is what makes it safe to hand an agent
the write API unsupervised.

## A document is not a file

- **file** — `notes.md`, `chart.html`
- **folder** — an entry point plus assets, served at `/d/{id}/`
- **zip** — the same, read straight out of the archive

Folders and zips both satisfy `fs.FS`, so they share one serving path. Each
bundle gets a URL prefix of its own, which is why relative `<script src>` and
`fetch()` inside a bundle resolve the way its author expected. A directory is a
bundle when it contains `index.html` (or `index.htm` / `main.html` / `app.html`);
otherwise it is just a namespace.

Quartzite **serves** bundles, it does not build them. No npm, no bundler —
bundles arrive pre-built and self-contained.

## Links

Content uses `[[Title]]`. The API uses stable IDs. Renaming rewrites inbound
links so references cannot rot, while the ID stays fixed. Unresolved links are
not errors: they render as broken and show in the dashboard count, because
writing toward a document that does not exist yet is a legitimate way to work.

## Viewers are documents

A document at `viewers/<format>.html` claims that format. It runs sandboxed
(`allow-scripts`, no same-origin), receives its target over `postMessage`, and
can render anything. Adding a format means writing a document — no Go code, no
restart. A broken viewer breaks its own pane and nothing else.

```js
addEventListener('message', (e) => {
  if (e.data.type !== 'quartzite:doc') return
  render(e.data.content)          // e.data.doc, e.data.links also provided
})
parent.postMessage({ type: 'quartzite:ready' }, '*')
```

To navigate the shell from inside a viewer:
`parent.postMessage({type:'quartzite:navigate', id}, '*')`.

## API

| method | path | notes |
|---|---|---|
| `GET` | `/api/docs` | `?source=store\|vault&kind=&format=&limit=` |
| `POST` | `/api/docs` | `{title, format, content}` or `{title, files:{...}}` for a bundle |
| `GET` | `/api/docs/{id}` | doc + content + resolved links + backlinks + viewer |
| `PUT` | `/api/docs/{id}` | `{content, file?}` — `file` targets one file in a bundle |
| `PATCH` | `/api/docs/{id}` | `{title}` — retitles and rewrites inbound links |
| `DELETE` | `/api/docs/{id}` | store only; vault returns 403 |
| `GET` | `/api/docs/{id}/raw` | raw bytes |
| `GET` | `/api/docs/{id}/backlinks` | inbound refs |
| `GET` | `/api/search?q=` | title > tag > path > body ranking |
| `GET` | `/api/stats`, `/api/metrics`, `/api/viewers` | dashboard data |
| `POST` | `/api/reindex` | force a rescan |
| `GET` | `/api/events` | SSE: `doc:created`, `doc:updated`, `doc:renamed`, `doc:deleted`, `index` |
| `GET` | `/d/{id}/...` | serve a document and its assets |

Create a note:

```bash
curl -X POST localhost:5003/api/docs -H 'Content-Type: application/json' \
  -d '{"title":"Rendering Notes","format":"md","content":"See [[Welcome]]."}'
```

Create a multi-file bundle:

```bash
curl -X POST localhost:5003/api/docs -H 'Content-Type: application/json' \
  -d '{"title":"Voxel Planet","files":{
        "index.html":"<canvas id=c></canvas><script src=\"sim.js\"></script>",
        "sim.js":"// served at /d/{id}/sim.js"}}'
```

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
```

The store itself is not here — it is your data, in whatever folder you point
`-store` at.

Indexing is a full rescan on a 3s timer plus after every write — ~170ms for 780
documents, which is cheaper than the dependency a file watcher would cost.

## Not in v1

Daily notes, calendars, graph visualisation, in-browser editing. v1 proves the
write loop and the format model.
