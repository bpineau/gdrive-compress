# AGENTS.md - onboarding for coding agents

Read this first, then `README.md`, which is the complete user manual (in
French: OAuth setup walkthrough, usage, the revision trap, what is
deliberately out of scope). It is current and its recipes must stay true.

## What this is

A one-shot personal tool: it recompresses the JPEG files already stored in the
author's Google Drive, in place, to free quota. It lists JPEGs over a size
threshold, shells out to ImageMagick's `magick` to find the highest quality
factor that fits under `--max-kib` (binary search over [40, 92], then
progressive downscale), and replaces the file content with `files.update`,
restoring the original `modifiedTime`. One `main` package, about 780 lines, no
tests, no Makefile: the size is the point, do not grow a framework around it.

Progress is an append-only JSONL log (`processed.jsonl`), which is what makes a
run resumable after a crash or a Ctrl-C. Lines carrying an `error` are ignored
on reload, so a rerun retries exactly the failures.

## Secrets: the rule that matters most

The repository is public (`github.com/bpineau/gdrive-compress`). Three
sensitive files live untracked in the working tree and are covered by
`.gitignore`:

- `credentials.json` - the Google OAuth client downloaded from the Cloud Console.
- `token.json` - the access/refresh token (kept in `~/Library/Application
  Support/gdrive-compress/`, not here).
- `processed.jsonl` - personal Drive file names and ids.
- `samples/` - personal photos written by `--dump-dir`.

**Never `git add -A` in this repo, never print the contents of those files, and
never remove their `.gitignore` entries.** Verified clean as of this file: `git
log --all --full-history` shows none of them was ever committed. If you ever
find one tracked, stop and report it rather than trying to scrub history.

## Build, run, verify

```sh
brew install imagemagick          # `magick` is a hard runtime dependency
go build -o gdrive-compress .     # the binary is gitignored
gofmt -l . && go vet ./...        # the whole gate; there are no tests
```

The cheap loop is the tool's own dry run, which is the default:

```sh
./gdrive-compress --quota                       # live storage usage, no writes
./gdrive-compress --limit 5 --dump-dir /tmp/cmp # 5 files, writes {orig,new} pairs locally
./gdrive-compress --apply --limit 5             # the first real writes: keep --limit
```

`--apply` is the only flag that mutates Drive. Always exercise a change with
`--limit` first, and inspect the pairs `--dump-dir` writes before widening.

## The tree

| File | Owns |
|---|---|
| `main.go` | flags, the worker pool (`--concurrency`, default 3), the per-file decision, the summary |
| `auth.go` | the OAuth dance and the token file (0600, in a 0700 user config dir) |
| `drive.go` | the Drive API calls: paginated list, content update, quota |
| `compress.go` | the `magick` subprocess: quality binary search then downscale ladder |
| `log.go` | the resumable JSONL progress log, serialized by a mutex |

## Traps

- **Replacing content does not free quota immediately.** Drive keeps the old
  revision for 30 days, so `--quota` stays high after `--apply`. Deleting
  revisions (`revisions.list` + `revisions.delete`) is not implemented; the
  README names `--purge-revisions` as the flag to add if it ever is.
- **EXIF must survive**: the `magick` invocation deliberately has no `-strip`.
  Do not add one.
- **The original `modifiedTime` is re-sent on update** so Drive's timeline and
  sort order are not disturbed by a recompression.
- **A recompression that is not smaller is skipped**, not applied.
- Retries are exponential on 429 and 5xx, up to 5 attempts. The scope requested
  is full `drive` (read + write on everything), because replacing existing
  content requires it: treat any change to the scope as a security decision.
- The module path is `github.com/ben/gdrive-compress` while the remote is
  `github.com/bpineau/gdrive-compress`. Harmless for a `main`-only tool that is
  never imported, but do not "fix" it casually: it changes nothing and rewrites
  every import line.
