# AGENTS.md - onboarding for coding agents

Read this first, then `README.md`, which is the complete user manual (in
French: OAuth setup walkthrough, usage, the revision trap, what is
deliberately out of scope). It is current and its recipes must stay true.

## What this is, and what it is for

**The problem it solves**: a Google Drive account has run out of free quota
because it holds thousands of phone photos at full size, and buying more
storage or deleting memories are both unattractive. This tool recompresses
JPEGs that are ALREADY in Drive, in place, so the quota comes back and the
files, their names, their ids and their dates stay exactly where they were.

How: it lists JPEGs over a size threshold, shells out to ImageMagick's `magick`
to find the highest quality factor that fits under `--max-kib` (binary search
over [40, 92], then a progressive downscale ladder), and replaces the file
content with `files.update`, restoring the original `modifiedTime`.

Who consumes it: one person at a terminal, against their own Drive. There is no
library, no API and no sibling repo: nothing depends on this and it depends on
nothing but the Google client libraries and `magick`.

One `main` package, a few hundred lines, no tests, no Makefile. **The size is
the point**: do not grow a framework around it.

Progress is an append-only JSONL log (`processed.jsonl`), which is what makes a
run resumable after a crash or a Ctrl-C. Lines carrying an `error` are ignored
on reload, so a rerun retries exactly the failures.

**Deliberately NOT in scope** (also stated in `README.md`): formats other than
JPEG, Google Photos (a different API and a different quota), a daemon or a
schedule, a GUI, and deleting old revisions, which is the one addition that has
been specified but not written (see the traps).

## Priorities and non-negotiables

1. **This tool destroys originals in someone's photo library.** Every change is
   judged on that: a bug here is not a wrong number, it is a lost memory.
   `--apply` is the only flag that writes, dry run is the default, and it must
   stay that way. Exercise any change with `--limit` and `--dump-dir` first.
2. **Never commit a secret or a personal file.** See the section below; it is
   the rule that matters most in this repo.
3. **EXIF must survive** and the original `modifiedTime` must be restored. A
   recompression that loses either is worse than no recompression.
4. **Never enlarge the OAuth scope** without treating it as a security
   decision. It is already full `drive` (read and write on everything), which
   is the minimum that allows replacing existing content.
5. **Keep it one flat `main` package.** No packages, no framework, no
   dependency injection. If a change needs an abstraction, it probably does not
   belong in this tool.
6. **English for code and this file**; `README.md` is French and stays French.
   **Never a typographic dash** in either.

## Secrets: the rule that matters most

The repository is public (`github.com/bpineau/gdrive-compress`). Three
sensitive files live untracked in the working tree and are covered by
`.gitignore`:

- `credentials.json` - the Google OAuth client downloaded from the Cloud Console.
- `token.json` - the access/refresh token (kept in `~/Library/Application
  Support/gdrive-compress/`, not here).
- `processed.jsonl` - personal Drive file names and ids.
- `samples/` - personal photos written by `--dump-dir`.

**Never `git add -A` in this repo** (those files sit untracked in the working
tree, one slip of a `-f` or a changed `.gitignore` and they are published),
**never print the contents of those files, and never remove their `.gitignore`
entries.** Stage by explicit path, always. `git log --all --full-history` shows
none of them was ever committed; if you ever find one tracked, stop and report
it rather than trying to scrub history yourself.

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
- **A `--dump-dir` run writes real photos into the working tree** (`samples/`).
  They are gitignored; leave them there or delete them, never stage them.

## Definition of done

- [ ] `gofmt -l .` silent and `go vet ./...` clean. That is the whole gate:
      there are no tests, so the real check is the next line.
- [ ] The change exercised against Drive with `--limit` and `--dump-dir`, and
      the `{orig,new}` pairs looked at, BEFORE any wide `--apply`.
- [ ] EXIF still present in a produced file, and `modifiedTime` unchanged on
      Drive, if anything near `compress.go` or `drive.go` moved.
- [ ] Nothing staged but source and docs: `git add <path>`, never `git add -A`.
      No secret, no `processed.jsonl`, no photo, no home path in the diff.
- [ ] `README.md` updated in the same commit if a flag, a default or a recipe
      changed; it is the user manual and it is French.
- [ ] No typographic dash in the diff.
- [ ] Committed to `master` and pushed. There is no CI: the local check is all
      there is.
- [ ] Anything the human must run by hand said explicitly, in particular that
      quota only comes back once Drive drops the old revisions.
