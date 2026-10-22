# AGENTS.md

Instructions for AI agents working in this repository.

## Onboarding journeys (optional)

Some contributors keep a private learning journey at `misc/JOURNEY.md`. Most
won't. Check for the file silently; **if it doesn't exist, skip this section
entirely** — don't mention journeys or the check itself (not even "no journey
file found"), don't offer to create one, just work normally.

If it exists, someone is learning this codebase with you as study partner.
Read it **in full before your first reply** when the person:

- says they're new, onboarding, or "starting a session," or
- asks how something in Quartzite works and the journey is in progress.

Then follow the **Rules for the agent** inside that file, in the mode the
plan names. They override your default helpfulness: you let the oracle
(build, running server, browser) decide what's true, you ask before you tell
wherever the mode calls for it, and you log the session in the file before it
ends. The **Next Session** section is the plan — start from its **Progress**
line and work through it in order, one step at a time.

Read the journey *before* exploring the repo yourself: the first reply should
be the first step of the plan, not a tour.

Not in a journey session? Ignore `misc/` and work normally.

`misc/` is gitignored on purpose: a journey is one learner's record, not
project documentation. Never commit it.

## Setting it up for someone

If a person asked you to install and run Quartzite for them (the README's
quickstart), they want to keep what gets written, so don't use a scratch
store.

- Build: `go build -o quartzite ./cmd/quartzite`. If `go` is missing, install
  Go 1.26+ first (`brew install go` on macOS).
- Run: `./quartzite -vault <their notes folder>`. The store defaults to
  `./store` in the clone; ask before choosing somewhere else.
- Never point `-store` at the vault, at a folder inside it, or at a folder
  that contains it.
- Then tell them to open `http://localhost:5003`. For writing documents, read
  `docs/api.md`.

## Working on the code

- Go 1.26+, **standard library only.** Never add a `require` to `go.mod`.
- Build: `go build -o quartzite ./cmd/quartzite`. Check: `go vet ./...`.
- Run against a scratch store, never the repo: `./quartzite -store "$(mktemp -d)/store"`.
  The store path must not exist yet if you want it seeded.
- There is no test suite yet. Verify changes by building and exercising the
  running server with `curl` or the browser.
- Nothing may write to the vault. There is no code path that opens a vault
  file for writing; keep it that way.
- Commit messages: `Area: what changed` (e.g. `Shell: …`, `Docs: …`,
  `Seed store: …`), with a body that explains why.
- Layout and internals: `docs/architecture.md`. API and viewer protocol:
  `docs/api.md`.
