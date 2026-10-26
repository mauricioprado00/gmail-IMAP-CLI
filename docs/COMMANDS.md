# gic — Gmail CLI

`gic` is a Go CLI for searching, labeling, and copying email between Gmail
accounts over IMAP, and for sending, replying to and forwarding mail over
SMTP (App Password auth for both). This is v2 of the spec, updated
per your answers. Anything still marked **[PROPOSAL]** is a design filling a
gap — flag it if you want it changed. Nothing is implemented yet.

---

## 1. Concepts

### 1.1 Accounts

Each Gmail account's config/secrets live in one JSON file, named
`<alias>.json`, stored in one of two **scopes**:

- **local** — `./accounts/<alias>.json`, resolved by searching upward from
  the current working directory for a directory literally named
  `accounts/` (like how `.git` is discovered) — **[PROPOSAL]**: stop at the
  first `accounts/` found walking up from CWD; if none exists anywhere up
  to `$HOME` or `/`, treat "local" as unavailable (fall through to global,
  or error, depending on the command — see resolution order below).
- **global** — `~/.config/gic/accounts/<alias>.json` (user-space wide, not
  system-wide, per your clarification — not `/etc`).

```json
{
  "alias": "mauricioprado00",
  "email": "mauricioprado00@gmail.com",
  "imapHost": "imap.gmail.com",
  "imapPort": 993,
  "smtpHost": "smtp.gmail.com",
  "smtpPort": 465,
  "appPassword": "xxxx xxxx xxxx xxxx",
  "scope": "local",
  "createdAt": "2026-08-28T09:00:00Z",
  "updatedAt": "2026-08-28T09:00:00Z"
}
```
`smtpHost`/`smtpPort` are optional: when absent (account files written
before sending existed) they default to `smtp.gmail.com:465`. Port 465 is
implicit TLS; any other port (e.g. 587) is plain TCP upgraded with
`STARTTLS`. The same App Password authenticates both IMAP and SMTP.

**[PROPOSAL]**: add a `scope` field to the JSON itself (redundant with
*where* the file lives, but handy for `account list --json` and for sanity
checks if a file is ever copied between the two directories by hand).

#### Choosing local vs. global on `account add`

```
gic account add [alias] --local     # default
gic account add [alias] --global
gic account add [alias] -g          # shorthand for --global
```
**[PROPOSAL]**: default is `--local` (matches current behavior); `--global`
opts into `~/.config/gic/accounts/`. Only one of `--local`/`--global` may be
given.

#### Resolution order (list / select / -s / every other command)

Since accounts can now live in two places, commands that need to find an
account merge both scopes:

1. Collect accounts from **local** `accounts/` (if found) and **global**
   `~/.config/gic/accounts/` (if it exists).
2. If the same alias exists in both, **local shadows global** (only the
   local one is shown/used) — same intuition as project-local config
   overriding user-global config in other tools.
3. The merged set is sorted by alias for `-s <N>` numbering, exactly as
   `gic account list` prints it (recomputed every call, no stored IDs).

`gic account list` marks each entry's scope:
```
$ gic account list
1. mauricioprado00@gmail.com (mauricioprado00) [local]
2. someone@gmail.com (work) [global]
```
**[PROPOSAL]**: `--local-only` / `--global-only` flags on `account list` (and
implicitly on resolution for `-s`/`select`, e.g. `-s global:work`) to
disambiguate two same-alias accounts if you ever want to bypass the
shadowing rule. Flag if you'd rather just rely on renaming to avoid
collisions instead of adding this extra syntax.

- File written with `0600` permissions in either scope.
- `alias` defaults to the full email address when not given to `account add`.
- Removing an account is just deleting its file from whichever scope it's
  in — no `account remove` command (confirmed, dropped from spec).

### 1.2 Session state via environment variables

You asked for env vars instead of PID-keyed session files. The problem:
a child process (the `gic` binary) cannot modify its parent shell's
environment directly. The standard fix (used by tools like `direnv`,
`pyenv`, `kubectl`) is:

- `gic select <alias|N>` and `gic mailbox select <name>` **print shell
  export statements to stdout** instead of mutating anything themselves.
- You wrap the real binary in a tiny shell function (installed once) so
  that typing `gic select 1` "just works" transparently, without you typing
  `eval $(...)` yourself.

**Setup (one-time):**
```
$ gic shell-init >> ~/.bashrc      # or ~/.zshrc
$ source ~/.bashrc
```

`gic shell-init` prints:
```bash
gic() {
  case "$1" in
    select|unselect|mailbox)
      eval "$(command gic-bin "$@")"
      ;;
    *)
      command gic-bin "$@"
      ;;
  esac
}
```
(The real compiled binary is installed/named `gic-bin`; `gic` becomes this
shell function and an executable wrapper for scripts. The wrapper starts a
private connection-pooling daemon on demand, with at most two connections per
account and a 60-second idle TTL; the daemon exits when its pool is empty.
`gic-bin select ...` alone — e.g. run from a script — just
prints the `export ...` lines instead of erroring, so it degrades cleanly.)

**Environment variables used:**

| Var | Set by | Meaning |
|---|---|---|
| `GIC_ACCOUNT` | `gic select <alias\|N>` | Currently selected account alias |
| `GIC_MAILBOX` | `gic mailbox select <name>` | Currently selected mailbox/label (default `INBOX` if unset) |

```
$ gic select 1
$ echo $GIC_ACCOUNT
mauricioprado00

$ gic mailbox select "[Gmail]/All Mail"
$ echo $GIC_MAILBOX
[Gmail]/All Mail

$ gic unselect            # clears GIC_ACCOUNT
```

Because these are plain env vars, they're naturally scoped per-shell/tab
(exactly like your original per-session goal) with zero extra state files,
and they survive/compose normally with subshells, scripts, `env -i`, etc.

### 1.3 One-off overrides (flags)

Every command accepts:
- `-s <alias|N>` — override `$GIC_ACCOUNT` for this invocation only.
- `-m, --mailbox <name>` — override `$GIC_MAILBOX` for this invocation only.

```
gic -s 1 -m "[Gmail]/All Mail" search label accountant
```

If neither the env var nor the flag resolves an account (for
account-required commands), `gic` exits with an error telling you to run
`gic select` or pass `-s`. Mailbox defaults to `INBOX` if fully unresolved.

---

## 2. `gic account` — manage accounts

### `gic account add [alias] [--local|--global|-g]`
Prompts interactively for the Gmail address and app password (hidden
input), opens an IMAP connection to `imap.gmail.com:993`, and does a
`SELECT INBOX` to validate before saving. Alias defaults to the email
address. Fails without writing the file if login/validation fails.
`--local` (default) saves under `./accounts/`; `--global`/`-g` saves under
`~/.config/gic/accounts/` (see §1.1).

```
$ gic account add work
Gmail address: someone@gmail.com
App password: ****************
✓ Verified IMAP login for someone@gmail.com
✓ Saved accounts/work.json

$ gic account add personal --global
Gmail address: me@gmail.com
App password: ****************
✓ Verified IMAP login for me@gmail.com
✓ Saved ~/.config/gic/accounts/personal.json
```

### `gic account list`
```
$ gic account list
1. mauricioprado00@gmail.com (mauricioprado00) [local]
2. someone@gmail.com (work) [local]
3. me@gmail.com (personal) [global]
```
If `alias == email`, prints just `1. mauricioprado00@gmail.com [local]` (no
redundant parenthetical). Merges local + global per the resolution order
in §1.1 (local shadows global on alias collision).

### `gic account rename <old-alias> <new-alias>`
Renames the JSON file **within its current scope**, updates its `alias`
field. Fails if `new-alias` already exists **in that same scope** (an alias
may still collide across scopes, in which case local shadows global per
§1.1). **Note:** since selection now lives in `$GIC_ACCOUNT` (a plain
string you set yourself), a rename does *not* retroactively fix any shell
where you already `export`ed the old alias — you'll need to
`gic select <new-alias>` again in open shells. This will be called out in
the rename command's output.

*(`gic account remove` intentionally omitted — just delete the JSON file
from whichever scope it's in.)*

---

## 3. `gic select` / `gic mailbox select`

Covered in §1.2. Quick reference:

```
gic select <alias|N>          # export GIC_ACCOUNT=<alias>
gic unselect                  # unset GIC_ACCOUNT
gic mailbox select <name>     # export GIC_MAILBOX=<name>
gic mailbox unselect          # unset GIC_MAILBOX (defaults back to INBOX)
gic mailbox list              # list mailboxes/labels on the selected account
```

### `gic mailbox list`
Lists all mailboxes/labels available on the selected account (via IMAP
`LIST`), numbered like `account list`, marking the currently selected one:

```
$ gic mailbox list
1. INBOX (selected)
2. [Gmail]/All Mail
3. [Gmail]/Sent Mail
4. [Gmail]/Trash
5. accountant
6. reviewed
```
Supports `-s <alias|N>` to list mailboxes for a non-selected account
without switching to it. **[PROPOSAL]** `--json` prints the raw list as a
JSON array of `{ "name": ..., "flags": [...] }` instead of the numbered
text form.

---

## 4. `gic search` — find messages

Searches the selected account + mailbox and prints a JSON array to stdout.

### 4.1 Combining filters — proposed solution

Flags are the primary interface (combinable); the single-filter positional
form you originally showed is kept only as shorthand sugar for the common
one-filter case, and internally maps to the same flag.

**Combination rule** (standard convention, e.g. similar to `git log`):
- **Different flags → AND.** `--from a --label b` matches messages that are
  both from `a` AND labeled `b`.
- **Repeated same flag → OR.** `--label a --label b` matches messages
  labeled `a` OR `b`.
- `--body`/`--subject` are logically AND'ed with everything else, but you
  can still repeat them for an OR within that field:
  `--subject invoice --subject receipt`.

| Flag | Example | IMAP mapping |
|---|---|---|
| `--from <addr>` | `--from someone@x.com` | `FROM` |
| `--to <addr>` | `--to someone@x.com` | `TO` |
| `--subject <text>` | `--subject "Invoice"` | `SUBJECT` |
| `--body <text>` | `--body "invoice #123"` | `BODY` |
| `--label <name>` | `--label accountant` | Gmail `X-GM-RAW label:accountant` |
| `--header <name>=<value>` | `--header X-GM-THRID=123456789` | `HEADER <name> <value>` |
| `--since <date>` | `--since 2026-01-01` | `SINCE` |
| `--until <date>` | `--until +5days` | `BEFORE` |
| `--seen` | `--seen` | `SEEN` |
| `--unseen` | `--unseen` | `UNSEEN` |

Dates accept `YYYY-MM-DD` or one or more signed offsets from today. Supported
units are `day`, `week`, `month`, and `year`, with singular or plural names.
For example, `+5days` means five days from now and `-1month -1week` means one
month and one week ago. `--until` is an exclusive upper bound because it maps
to IMAP's `BEFORE`; `--before` remains available as an alias.

```
# shorthand (single filter)
gic search label accountant
gic search subject "whatever"

# flag form (combinable, AND across flags / OR within repeats)
gic search --from someone@x.com --label accountant --subject "Invoice"
gic search --label accountant --label reviewed        # label=a OR label=b
gic search --header X-GM-THRID=1234567890123456789
gic search --since -1month -1week --until +1day
gic search --unseen --label accountant                # unseen AND labeled
```

`--seen`/`--unseen` filter on the IMAP `\Seen` system flag (what Gmail
presents as read/unread) and are mutually exclusive; omitting both matches
messages in either state. `--read`/`--unread` are accepted as aliases.

Other flags:
- `--all-mailboxes` — search across every mailbox instead of just
  `$GIC_MAILBOX`/`-m` (Gmail: effectively `[Gmail]/All Mail`).
- `--limit <N>` — cap number of results.
- `--mode <light|standard|full>` — see §4.2 (default `standard`).

### 4.2 Retrieval modes

Controls IMAP `FETCH` cost/detail, per your request for lighter/heavier
options:

| Mode | IMAP items fetched | Contents |
|---|---|---|
| `light` | `FLAGS`, `ENVELOPE`, `RFC822.SIZE` | Headers-only metadata: from/to/subject/date/message-id/flags/size. No body, no attachments. Fastest, used for large bulk scans. |
| `standard` *(default)* | + `BODY.PEEK[TEXT]` of non-attachment parts (via `BODYSTRUCTURE` walk) | Adds plain-text/HTML body content, excludes attachment parts. |
| `full` | `BODY.PEEK[]` (entire raw RFC822 message) | Everything, including attachments, base64-encoded as in the raw message. Needed before `gic copy`/`gic fetch` re-append elsewhere. |

```
gic search label accountant --mode light
```


### 4.3 Output (JSON)

```json
[
  {
    "uid": 4821,
    "mailbox": "[Gmail]/All Mail",
    "messageId": "<abc123@mail.gmail.com>",
    "threadId": "1234567890123456789",
    "from": "someone@x.com",
    "to": ["mauricioprado00@gmail.com"],
    "subject": "Invoice #123",
    "date": "2026-08-01T10:00:00Z",
    "labels": ["accountant", "INBOX"],
    "flags": ["\\Seen"],
    "seen": true,
    "size": 20481,
    "body": { "text": "...", "html": "..." },
    "raw": "<base64, only in full mode>"
  }
]
```
`body`/`raw` keys are present only for `standard`/`full` modes respectively.
`flags` lists the raw IMAP system flags and `seen` is the convenience
boolean for `\Seen` (what Gmail presents as read), available in every mode.
This JSON is meant to be piped into `gic label apply`, `gic mark`,
`gic fetch`, or `gic copy`.

---

## 5. `gic label` — manage & apply Gmail labels

### `gic label list`
Lists all labels/mailboxes on the selected account.

### `gic label create <name>`
Creates a Gmail label if it doesn't already exist (no-op success if it
does).

### `gic label apply <name> --uid <uid>[,...]`
Applies a label to one or more messages by UID; auto-creates the label
unless `--no-create` is passed.

```
gic search label accountant | gic label apply reviewed --stdin
```
`--stdin` reads a JSON array (the `search` output shape) from stdin and
applies the label to every message in it, instead of `--uid`.

### `gic label remove <name> --uid <uid>[,...]`
Removes a label from messages (does not delete the label itself).

---

## 6. `gic mark` — set / clear the `\Seen` flag

Adds or removes the IMAP `\Seen` system flag, which is what Gmail presents
as read vs. unread. Like `gic label apply`, targets come from either
`--uid` or `--stdin`.

### `gic mark seen [--uid <uid>[,...] | --stdin]`
Sets `\Seen` on the given messages (Gmail shows them as read).

### `gic mark unseen [--uid <uid>[,...] | --stdin]`
Clears `\Seen` on the given messages (Gmail shows them as unread).

```
gic mark seen --uid 4821,4900
gic mark unseen --uid 4821

# pipe search results straight in
gic search --unseen --label accountant | gic mark seen --stdin
gic search --from boss@x.com --seen | gic mark unseen --stdin
```
`--stdin` reads a JSON array (the `search` output shape) from stdin and
marks every message in it. Both subcommands operate on the selected
mailbox (`$GIC_MAILBOX`/`-m`, default `INBOX`), since IMAP flags are
per-mailbox. Use `gic search`'s `seen` field (§4.3) to verify the result.

`read`/`unread` are accepted as aliases for `seen`/`unseen`.

---

## 7. `gic fetch` — download messages

```
gic fetch --uid 4821 -o ./out/4821.eml
gic search label accountant --mode full | gic fetch --stdin -o ./out/
```
Downloads full raw RFC822 message(s) to disk, one `.eml` file per message
when given multiple (named by UID). `-o -` writes raw bytes to stdout for a
single message.

---

## 8. `gic attachment remove` — remove attachments

Removes all attachments or only attachments with specified filenames from
one or more messages:

```sh
# Preview every attachment that would be removed.
gic attachment remove --uid 4821 --all --dry-run

# Remove every attachment.
gic attachment remove --uid 4821 --all

# Remove selected filenames (case-insensitive exact matches).
gic attachment remove --uid 4821 \
  --name IMG_20210606_115119.jpg \
  --name video.mp4

# Consume normal search-result JSON.
gic search --subject "Old files" --mode light |
  gic attachment remove --stdin --all --dry-run
```

Exactly one selection mode is required: `--all`, or one or more repeatable
`--name` flags. `--uid` and `--stdin` select messages in the current mailbox
(`$GIC_MAILBOX`/`-m`, default `INBOX`). UIDs are mailbox-specific, so use the
same mailbox that produced the search results.

For each changed message, gic:

1. Downloads the full MIME message.
2. Removes the selected attachment parts.
3. Adds one visible line per removed file at the end of the message, such as
   `IMG_20210606_115119.jpg removed with gic`.
4. Gives the replacement a unique Message-ID and trace header, then appends it
   with the original internal date, flags, and mutable Gmail labels.
5. Fetches the replacement back and verifies its UID and trace header.
6. Permanently removes the original through Gmail's All Mail and Trash
   mailboxes only after the replacement and labels succeed.

If a previous run created and verified a replacement but could not remove the
original, rerunning the same command reuses that replacement instead of
creating another copy. Any extra verified replacements from earlier retries
are removed during recovery.

Gmail derives `\All`, `\Sent` and `\Draft` from the message itself and rejects
them on `STORE X-GM-LABELS`, so a replacement is not restored to Sent or
Drafts. To keep a sent message in Sent, run the command with that mailbox
selected (`-m "[Gmail]/Sent Mail"`) so the replacement is appended there.

The replacement receives a new UID and Message-ID. The original Message-ID is
retained in `X-GIC-Original-Message-ID`. The JSON result includes both the
original `uid` and `replacementUid`, plus each removed attachment's decoded
size and an `attachmentsRemoved` count.
`--dry-run` performs no APPEND, STORE, or EXPUNGE operations.

This operation requires the server's `UIDPLUS` extension so gic can identify
the replacement and expunge only the original message. If replacement cleanup
fails, gic reports the error rather than silently claiming success.

---

## 9. `gic copy` — cross-account copy

```
gic copy --from-account mauricioprado00 --to-account work \
  --label accountant \
  --apply-label imported \
  --to-mailbox "[Gmail]/All Mail"
```

Steps:
1. Runs the equivalent of `gic -s <from-account> search <filters> --mode full`.
2. For each match, appends the raw message to the destination account's
   target mailbox via IMAP `APPEND`, preserving `INTERNALDATE`/flags where
   possible.
3. If `--apply-label` is given, applies that label on the destination after
   append.
4. Prints a JSON summary (counts + per-message success/failure).

Supports all `gic search` filter flags, plus:
- `--dry-run` — preview matches (light mode) without appending anything.
- `--skip-duplicates` — skip messages whose `Message-ID` already exists in
  the destination mailbox (idempotent re-runs).

---

## 10. `gic send` / `gic reply` / `gic forward` — send mail

Sends mail over SMTP with the selected account's credentials. Gmail files
anything sent through its SMTP server into `[Gmail]/Sent Mail` on its own, so
no IMAP `APPEND` step is involved. SMTP sessions are short-lived and go
straight to the server — they never pass through the connection-pooling
daemon. The `From` address is always the account's email.

Everything here is irreversible once the server accepts the message, so all
three commands take `--dry-run`, which builds the full RFC 5322 message and
prints it to stdout **without connecting to anything**. Recipients are only
ever taken from flags, never from stdin, so a mis-piped search result can
never fan out mail.

### `gic send`

```sh
gic send --to a@x.com --subject "Invoice" --body "Attached."
gic send --to a@x.com --to b@x.com --cc c@x.com --bcc me@x.com \
  --subject "Report" --body-file report.txt --attach report.pdf --attach chart.png
cat body.txt | gic send --to a@x.com --subject "Notes" --body-file -
gic send --to a@x.com --subject "Test" --body "Hello" --dry-run > preview.eml
```

| Flag | Description |
|---|---|
| `--to <addr>` | Recipient (repeatable; a value may also be a comma-separated list). At least one `--to`, `--cc` or `--bcc` is required. Accepts `Name <addr>` form. |
| `--cc <addr>` | Carbon-copy recipient (repeatable). |
| `--bcc <addr>` | Blind recipient (repeatable). Included in the SMTP envelope, omitted from the headers. |
| `--subject <text>` | Subject (UTF-8 is RFC 2047-encoded when needed). |
| `--body <text>` | Plain-text body. Mutually exclusive with `--body-file`. |
| `--body-file <path>` | Read the plain-text body from a file; `-` reads stdin. |
| `--attach <path>` | Attach a file (repeatable). Content type is sniffed from the extension, falling back to `application/octet-stream`. |
| `--dry-run` | Print the message to stdout instead of sending. |

The body is sent as `text/plain; charset=utf-8` (quoted-printable). With
attachments the message becomes `multipart/mixed` with base64 parts. A fresh
`Message-ID` of the form `<random@gic>` is always generated.

On success gic prints a JSON summary:
```json
{
  "status": "sent",
  "messageId": "4f1c...@gic",
  "from": "mauricioprado00@gmail.com",
  "recipients": ["a@x.com", "c@x.com", "me@x.com"],
  "size": 1234
}
```

### `gic reply --uid <uid>`

```sh
gic reply --uid 4821 --body "Thanks, received."
gic reply --uid 4821 --all --body-file answer.txt
gic -m "[Gmail]/All Mail" reply --uid 4821 --body "ok" --dry-run
```

Fetches the single message `--uid` from the current mailbox (`$GIC_MAILBOX`/
`-m`, default `INBOX`) and builds a reply:

- **To** is the original `Reply-To`, or `From` when there is none.
- `--all` additionally copies the original `To` and `Cc` into `Cc`, dropping
  the account's own address and any duplicates.
- Extra `--to`/`--cc`/`--bcc` flags are added on top.
- **Subject** is the original prefixed with `Re: ` unless it already starts
  with `Re:` (case-insensitive).
- `In-Reply-To` is the original `Message-ID`; `References` is the original
  `References` plus its `Message-ID`, so Gmail threads the reply.
- The original's first `text/plain` part is quoted below the body as
  `On <date>, <from> wrote:` followed by `> `-prefixed lines. `--no-quote`
  omits it. HTML-only originals are not converted; the reply simply carries
  no quote.
- `--body`/`--body-file`/`--attach`/`--dry-run` behave as in `gic send`.

After the server accepts the reply, gic sets `\Answered` on the original
(the flag Gmail shows as the replied arrow). A failure to set the flag is
reported but does not undo the send — it cannot.

### `gic forward --uid <uid>`

```sh
gic forward --uid 4821 --to a@x.com
gic forward --uid 4821 --to a@x.com --note "FYI, see below."
gic forward --uid 4821 --to a@x.com --as-attachment
```

Fetches the single message `--uid` and forwards it to the given recipients
(at least one `--to`/`--cc`/`--bcc` is required):

- **Subject** is the original prefixed with `Fwd: ` unless it already starts
  with `Fwd:`/`Fw:` (case-insensitive).
- **Default (inline)**: the body is the optional `--note` text, a
  `---------- Forwarded message ----------` block with the original `From`,
  `Date`, `Subject` and `To`, and the original's first `text/plain` part. The
  original's attachment parts are carried over verbatim (same headers and
  encoded bytes, so nothing is re-encoded). HTML-only originals forward with
  an empty text section; use `--as-attachment` for those.
- `--as-attachment`: the whole original is attached untouched as a
  `message/rfc822` part, with `--note` as the text part. This is the
  lossless option.
- `--attach` adds further files; `--dry-run` previews.

### Verbose tracing

`-v` prints the SMTP dialogue to stderr like the IMAP trace. The `AUTH PLAIN`
line is redacted so the App Password never lands in a terminal scrollback.

---

## 11. Global flags (all commands)

| Flag | Description |
|---|---|
| `-s <alias\|N>` | One-off account override (§1.3) |
| `-m, --mailbox <name>` | One-off mailbox override (§1.3) |
| `-v, --verbose` | Print IMAP/SMTP protocol trace to stderr |

---

## 12. Remaining open items

- §1.1's **local/global account scope** design is new — please confirm:
  - Is walking up from CWD to find `accounts/` the right lookup (vs. only
    checking the exact CWD, as before)?
  - Is "local shadows global" the right collision rule, or would you
    rather collisions be an error requiring a rename?
  - Do you want the `--local-only`/`--global-only` disambiguation flags, or
    skip them for now?

Once confirmed, I'll implement the local/global scope changes in
`internal/account` and `internal/cli` (everything else in this doc is
already implemented and working).
