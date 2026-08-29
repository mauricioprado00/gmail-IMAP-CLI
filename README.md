# gic — Gmail IMAP CLI

See [`docs/COMMANDS.md`](docs/COMMANDS.md) for the full command reference.

## Building

The local `go` on this machine is a wrapper script
(`/usr/local/bin/go` → `~/linuxchanges/filesystem/usr/local/bin/go`) that
runs `docker run -it ... golang:1.8`. Two things make plain `go build`
fail here:
1. `-it` requires a real TTY — it errors with `cannot attach stdin to a
   TTY-enabled container because stdin is not a terminal` in any
   non-interactive shell (scripts, CI, this agent's shell, etc.).
2. Even when it does run, it's Go **1.8** — far too old for this module
   (`go.mod` requires 1.23+ for `net/mail`/generics-adjacent stdlib used
   here).

So **don't use the bare `go` command** in this project. Use one of:

**Option A — `make` (recommended):**
```sh
make build   # -> bin/gic-bin
make test
make vet
make install # copies bin/gic-bin to ~/.local/bin/gic-bin
```

**Option B — `./gorun.sh` directly** (what `make` calls under the hood): a
plain, non-interactive `docker run --rm` against `golang:1.23`, with a
named volume (`gic-gomodcache`) so module downloads are cached across
runs:
```sh
./gorun.sh build -buildvcs=false -o bin/gic-bin ./cmd/gic-bin
./gorun.sh test ./... -timeout 30s
./gorun.sh vet ./...
```
`-buildvcs=false` is required because the container's git can't read this
host repo's ownership metadata (`error obtaining VCS status: exit status
128` otherwise) — harmless to omit the VCS build stamp.

Either way, the compiled binary ends up at `./bin/gic-bin` in this project
directory — it is **not** installed system-wide by `build`/`gorun.sh`
alone; run `make install` (or copy it yourself) to put it on your `$PATH`.

**Requirements:** Docker must be installed and able to pull images (first
run downloads the `golang:1.23` image, ~350MB).

## Installing the shell wrapper

`gic select`/`gic mailbox select` need to modify your shell's environment,
which a child process can't do directly. Run `make install` first (see
above) to put the compiled binary on your `$PATH` as `~/.local/bin/gic-bin`
(make sure `~/.local/bin` is in `$PATH`, e.g. `export
PATH="$HOME/.local/bin:$PATH"` in your shell rc file), then install the
wrapper function once:

```sh
make install
gic-bin shell-init >> ~/.bashrc   # or ~/.zshrc
source ~/.bashrc
```

After that, `gic <command>` works transparently (see docs/COMMANDS.md §1.2).
The installed `gic` executable is an on-demand connection-pooling wrapper.
It starts a private local daemon when needed; this is not a system service.
The daemon keeps at most two authenticated connections per account, closes
connections after 60 seconds idle, and exits when its pool becomes empty.

## Quick start

```sh
gic account add work        # prompts for email + app password
gic account list
gic select work
gic mailbox list
gic search label accountant
gic search --from a@x.com --label accountant
gic search --since -1month -1week --until +1day
gic search --unseen                                  # only unseen messages
gic mark seen --uid 4821,4900                        # set \Seen
gic search --unseen --label accountant | gic mark seen --stdin
gic copy --from-account work --to-account personal --label accountant --dry-run
```

## Status / limitations

- Auth is Gmail App Passwords only (no OAuth2).
- `--mode standard`'s `body.text` is the raw `BODY[TEXT]` MIME section, not
  split into separate text/html parts.
- Hand-rolled minimal IMAP client (no third-party IMAP library) — covers
  LOGIN, LIST, SELECT, CREATE, UID SEARCH (incl. Gmail X-GM-LABELS/
  X-GM-THRID extensions), UID FETCH, APPEND, and UID STORE for labels and
  system flags (`\Seen`).
- Not yet tested against a live Gmail account (only unit-tested against
  fake in-process IMAP servers) — sandbox has no credentials to verify
  against. Please test `account add` / `search` / `copy` against a real
  account before relying on it, especially `copy`'s APPEND + label steps.
