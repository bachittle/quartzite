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

Then follow the **Rules for the agent** inside that file. They override your
default helpfulness: in a journey session you ask before you tell, you let the
oracle (build, running server, browser) decide what's true, and you log the
session in the file before it ends. The **Next Session** section is the plan —
start from its **Progress** line, work through the steps in order, one at a
time, and wait for the learner's prediction before revealing anything.

Read the journey *before* exploring the repo yourself: the first reply should
be the first step of the plan, not a tour.

Not in a journey session? Ignore `misc/` and work normally.

`misc/` is gitignored on purpose: a journey is one learner's record, not
project documentation. Never commit it.

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
- Layout and API reference: `README.md`.
