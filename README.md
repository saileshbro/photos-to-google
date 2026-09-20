# ptg — Apple Photos → Google Photos

Uploads photos and videos to Google Photos at original quality, keeping Apple
Live Photos as a single item. Built to be driven by a person at a terminal or by
an AI agent, with the same command surface for both.

```sh
go build -o ptg ./cmd/ptg

ptg auth login --clipboard        # once, after copying the oauth_token cookie
ptg upload ~/Pictures/export -r
```

## The output contract

Everything an agent needs is on stdout, one record per line. Progress, warnings
and the closing summary go to stderr, so a pipe only ever carries results.

```
STATUS<TAB>PATH<TAB>DETAIL[<TAB>KEY=VALUE...]
```

| STATUS | Meaning |
| --- | --- |
| `uploaded` | New item in Google Photos; DETAIL is its media key |
| `exists` | Identical bytes were already in the account |
| `skipped` | Deliberately not uploaded; DETAIL says why |
| `failed` | Google refused it or the transfer broke; DETAIL is the error |

With `--json`, stdout becomes NDJSON instead: one object per line, each with a
schema version `v`, an `event` (`result`, `progress`, `warning`, `summary`, …)
and the fields for that event. Progress moves to stdout in this mode, because a
program reading the stream wants it in order.

```sh
ptg upload ~/export -r --json | jq -r 'select(.status=="failed") | .path'
ptg upload ~/export -r | grep ^failed | cut -f2
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Everything asked for succeeded |
| 1 | The run completed, but at least one item failed |
| 2 | Bad usage: no paths, unreadable input, bad flags |
| 3 | No usable credentials — run `ptg auth login` |
| 130 | Interrupted |

## Input: files, folders, stdin

```sh
ptg upload a.heic b.mov                 # files
ptg upload ~/export -r                  # folders, recursively
find ~/export -name '*.HEIC' -print0 | ptg upload -0 -
ls *.jpg | ptg upload -                 # one path per line
ptg upload --dry-run ~/export -r        # list what would go, upload nothing
```

`ptg` never prompts. It reads stdin when given `-`, or when stdin is not a
terminal, so it behaves the same inside a script, a pipeline or an agent.

## Live Photos

A Live Photo is one item, not a photo next to a two-second video. `ptg` matches
the still and its `.mov` by the content identifier Apple embeds in both, uploads
both, and commits them as a single Google Photos item.

Two cases from a real 11,820-item migration are handled rather than reported as
errors:

- The still is already in Google Photos without its motion: the video is
  uploaded and attached to the existing item.
- The video is already there, because an edited copy of the same Live Photo went
  up first and the two share byte-identical video: the still is retried on its
  own.

## Rate limits

Google returns `429`, `500` and `503` under load; a long run will meet all
three. `ptg` retries failed items in later passes (`--retries`, default 2) and
waits longer when the errors look like overload — 60 s, doubling to 10 minutes —
instead of hammering through them.

## Credentials

`ptg auth login` exchanges a single-use Embedded Setup `oauth_token` for a
long-lived credential, stored in the config file (`--config` to place it
elsewhere). The token is never printed, and `--clipboard` clears the clipboard
after reading it. `ptg auth status` lists accounts; `ptg auth logout` removes
one.

## Layout

- `cmd/ptg` — entry point
- `internal/cli` — commands, output contract, retry and backoff
- `internal/gotohp` — vendored MIT core from [xob0t/gotohp](https://github.com/xob0t/gotohp)
  (see `NOTICE`), which implements the Google Photos mobile upload protocol
- `reference-python` — the scripts from the first full run, kept for reference

## Status

`auth` and `upload` are done. Next: `ptg photos list` and `ptg photos export`
(the macOS Photos library as a source) and `ptg serve` (the live progress page).
