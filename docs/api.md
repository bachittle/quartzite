# API

Everything an agent needs to read and write Quartzite over HTTP. The server
listens on `localhost:5003` by default. For how the pieces fit together, see
[architecture.md](architecture.md).

## Endpoints

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
| `GET` | `/api/health` | `{status, docs, time}` — is the server up |
| `POST` | `/api/reindex` | force a rescan |
| `GET` | `/api/events` | SSE: `doc:created`, `doc:updated`, `doc:renamed`, `doc:deleted`, `index` |
| `GET` | `/d/{id}/...` | serve a document and its assets |

Writes only ever touch the store. Vault documents answer 403 to `PATCH` and
`DELETE`, and 404 to `PUT`.

## Examples

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

Update one file in a bundle:

```bash
curl -X PUT localhost:5003/api/docs/<id> -H 'Content-Type: application/json' \
  -d '{"file":"sim.js","content":"// new version"}'
```

Retitle, rewriting every `[[Rendering Notes]]` that points at it:

```bash
curl -X PATCH localhost:5003/api/docs/<id> -H 'Content-Type: application/json' \
  -d '{"title":"Rendering"}'
```

Watch changes live:

```bash
curl -N localhost:5003/api/events
```

## Viewers and bundles

Documents run in a sandboxed iframe with no same-origin access, so they cannot
call the API. They talk to the shell with `postMessage`.

A viewer at `viewers/<format>.html` renders every document of that format:

```js
addEventListener('message', (e) => {
  if (e.data.type !== 'quartzite:doc') return
  render(e.data.content)          // e.data.doc, e.data.links also provided
})
parent.postMessage({ type: 'quartzite:ready' }, '*')
```

| message | direction | what it does |
|---|---|---|
| `quartzite:ready` | document → shell | ask for the target document |
| `quartzite:doc` | shell → viewer | `{doc, content, links}` |
| `quartzite:theme` | shell → document | `{name, tokens}` — the current palette, sent on load and on every theme change |
| `quartzite:navigate` | document → shell | `{id}` — open another document |
| `quartzite:save` | document → shell | `{reqId, file, content}` — rewrite a file in the document's *own* bundle (store only, ≤ 256 KB, no `..` or absolute paths) |
| `quartzite:save:result` | shell → document | `{reqId, ok}` or `{reqId, error}` |
