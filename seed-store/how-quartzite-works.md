---
title: How Quartzite Works
tags: [quartzite, architecture]
---

# How Quartzite Works

Start with [[Welcome]] if you want the interactive version. This note is the
written one, and it exists partly to prove that a markdown document and a
running React-style bundle sit in the same graph as equals.

## Two corpora

Quartzite indexes two places and merges them into one link graph.

| corpus | path | access | why |
|---|---|---|---|
| vault | `-vault` / `QUARTZITE_VAULT` (optional) | read-only | existing notes, worth linking to, not worth risking |
| store | `-store` / `QUARTZITE_STORE` | read-write | everything agents create |

"Vault" is just the conventional name for a folder of notes you already have —
an existing knowledge graph worth linking into. It is optional: with no vault
configured, Quartzite serves the store alone.

Nothing Quartzite does can write to the vault. That is not a policy setting,
it is the absence of any code path that opens a vault file for writing. It is
what makes it safe to hand an agent the write API without supervision.

## A document is not a file

Three shapes, one graph:

- **file** — `notes.md`, `chart.html`, a single self-contained thing
- **folder** — an entry point plus its assets, served at `/d/{id}/`
- **zip** — the same, read straight out of the archive

Folders and zips both satisfy Go's `fs.FS`, so they share one serving path.
A bundle gets a URL prefix of its own, which is what makes relative
`<script src>` and `fetch()` resolve the way the author expected.

## Links

Content links by `[[Title]]` — the wikilink convention. The API uses stable
IDs. Renaming a document rewrites inbound links so a reference cannot silently
rot, while the ID stays fixed for anything holding it.

An unresolved link is not an error — it renders as broken and shows up in the
dashboard count. Writing toward a document that does not exist yet is a
legitimate way to work.

## Viewers are documents

A document at `viewers/<format>.html` claims that format. It runs sandboxed,
receives its target over `postMessage`, and can render anything it likes.

```
viewers/md.html      -> renders every .md document
viewers/csv.html     -> would render every .csv document
viewers/fbp.html     -> would render a format you invented this afternoon
```

Adding a format means writing a document. No Go code, no restart, no registry
file to edit. A broken viewer breaks its own pane and nothing else, because it
has no same-origin access to the shell.

## Not in v1

Daily notes, calendars, graph visualisation, in-browser editing. v1 proves
the write loop and the format model — those had to be right first.
