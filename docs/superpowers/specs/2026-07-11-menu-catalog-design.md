# Menu catalog restructure for the setup scripts

Date: 2026-07-11
Status: approved

## Problem

The three local bootstraps (`local/bootstrap-linux.sh`, `local/bootstrap-macos.sh`,
`local/bootstrap-windows.ps1`) and `server/setup-server.sh` are single linear
flows: all inputs are collected up front, then every step runs in sequence. One
failure (a bad pasted key, a declined sudo, an unmanaged `Host` block the user
refuses to overwrite) aborts the whole run, including steps unrelated to the
failure, and re-running to fix one thing re-asks every question.

The steps are in fact almost entirely independent of each other. Restructure
each script as an interactive catalog: a numbered menu of independent items,
each selectable on its own, with its current state detected and displayed.

## Goals

- Each item can be run, re-run, skipped, or fail alone; an aborted item never
  affects the others.
- The menu shows detected per-item status, so a re-run makes it obvious what is
  left to do.
- Inputs are asked per item, at the moment the item runs — only what that item
  needs.

## Non-goals

- No behavior changes inside items beyond what this spec states: same keys,
  same config blocks, same hardening options, same CLAUDE.md content.
- No new files; each script stays self-contained (the three bootstraps remain
  deliberate near-copies of each other, per the repo's curl-one-liner design).
- No "full setup / run all" menu entry — the statuses make sequencing obvious.
- No non-interactive subcommand interface (env-var overrides cover automation).

## Script shape

All four scripts get the same structure:

- One pair of functions per catalog item:
  - `status_<item>` — cheap, read-only, reports done / not done for the menu.
  - `run_<item>` — performs the item, prompting only for its own inputs.
- A menu loop: draw the catalog with fresh statuses, read a selection, run the
  item, redraw. `q` quits.
- The big up-front input block is removed. Existing env-var overrides
  (`SERVER_HOST`, `SERVER_USER`, `SERVER_PORT`, `REVERSE_PORT`, `LOCAL_USER`,
  `SERVER_PUBKEY`, `LOCAL_PUBKEY`, `LOCAL_PROJECT_DIR`) and the Windows
  script's parameters keep working and skip the corresponding prompt.
- `local/bootstrap.sh` (the OS dispatcher) is unchanged.
- The `~/.ssh` directory/permission prep is not an item; it runs inside any
  item that touches `~/.ssh`.

## Local catalog (identical across Linux / macOS / Windows)

```
1) Incoming SSH — install/enable sshd + harden   [sudo]   [done]
2) Local SSH key (~/.ssh/id_ed25519)                      [done]
3) Authorize the server's connect-back key                [ - ]
4) Tunnel config (Host remote-claude)                     [ - ]
5) Show local public key (paste into server setup)
q) Quit
```

| # | Item | Inputs | Status check |
|---|------|--------|--------------|
| 1 | Install/enable sshd, harden config (pubkey on, optional password off) | disable-password y/n | sshd installed and running, and the managed hardening present (the drop-in file exists, or on systems without `sshd_config.d`, `PubkeyAuthentication yes` is set in the main config) |
| 2 | Ensure `~/.ssh/id_ed25519` exists (generate if missing) | — | key file exists |
| 3 | Append the server's pubkey to `authorized_keys`, loopback-restricted, dedup by blob | pasted server pubkey | an entry with `from="127.0.0.1,::1"` exists in `authorized_keys` |
| 4 | Write the managed `Host remote-claude` block in `~/.ssh/config` | server host, server user, server port, reverse port | managed `# >>> remote-claude >>>` block present |
| 5 | Print `~/.ssh/id_ed25519.pub` for the server-side handoff | — | action item, no status; requires the key (offers to run item 2 if missing) |

Only item 1 needs sudo/admin. Items 1–4 are mutually independent; item 5
depends on item 2. On Windows, the elevation check moves from script startup
into item 1, so the other items work from a non-elevated PowerShell.

## Server catalog (`setup-server.sh`)

```
1) Connect-back key — ensure + show public key            [done]
2) Authorize the local machine's key (tunnel login)       [ - ]
3) my-device ssh alias (Host block)                       [done]
4) Agent instructions (~/.claude/CLAUDE.md)               [ - ]
5) Agent facts file (facts.json)                          [ - ]
q) Quit
```

| # | Item | Inputs | Status check |
|---|------|--------|--------------|
| 1 | Ensure `~/.ssh/id_ed25519` exists and print its pubkey (paste into the local bootstrap) | — | key file exists |
| 2 | Append the local machine's pubkey to `authorized_keys`, dedup by blob | pasted local pubkey | a line tagged `remote-claude-tunnel` exists (see below) |
| 3 | Write the managed `Host my-device` block | reverse port, local username | managed `# >>> my-device >>>` block present |
| 4 | Install the managed CLAUDE.md block; create `~/tmp` | — | managed `<!-- >>> my-device >>> -->` markers present in `~/.claude/CLAUDE.md` |
| 5 | Seed `~/.config/remote-claude/facts.json` (never overwrites an existing file) | optional local project dir | facts file exists |

All five items are independent. Items 4 and 5 are split (today one y/n prompt
installs both).

**Item 2 detectability:** the appended key line gets a trailing
` remote-claude-tunnel` comment tag, and the status check greps for that tag.
A key authorized by other means (e.g. `ssh-copy-id`) shows not-done even
though it works; the item stays idempotent (dedup by key blob), so running it
again is harmless.

## Failure and abort semantics

- Item functions do not exit the script on error (`die` / `exit` inside item
  code is replaced): they report the error and return non-zero, and the loop
  drops back to the menu. Other items are unaffected.
- Ctrl-C still exits the whole script. Every item is independent and
  idempotent, so this is safe: on re-run the statuses show where things stand.
- All existing in-item safety behavior is kept: `*.claude-bak-<timestamp>`
  backups, `sshd -t` validation with rollback, marker-managed blocks,
  dedup-by-blob, pasted-key validation via `ssh-keygen -lf`.

## Autostart removal

The tunnel-autostart feature is deleted outright; the tunnel is only ever
started manually with `ssh -N remote-claude`.

- Delete from the bootstraps: the systemd user service section (Linux, incl.
  the lingering prompt), the LaunchAgent section (macOS), the Scheduled Task +
  keepalive-script section (Windows), and their mentions in headers/summaries.
- Delete the orphaned examples: `examples/remote-claude.service.example`,
  `examples/com.claude.dev-tunnel.plist.example`.
- Scrub the docs: README.md and README.zh-CN.md (step 3 wording, the
  "Stop / uninstall" autostart bullets), TROUBLESHOOTING.md and
  TROUBLESHOOTING.zh-CN.md (any autostart references).

## Documentation updates

Beyond the autostart scrub, update README (both languages) to describe the
menu: running a script presents the catalog, items are selected individually,
and re-running shows what is already done. Script header comments are
rewritten to describe the catalog items instead of the numbered linear steps.

## Testing

- Shell syntax: `bash -n` on the three bash scripts; PowerShell parse check
  on `bootstrap-windows.ps1` where available.
- Manual matrix on Linux (the available platform): fresh run of each item,
  re-run idempotence, status accuracy after each item, failure inside an item
  returning to the menu, env-var overrides skipping prompts.
- macOS and Windows scripts are reviewed for parity by diff against the Linux
  structure (the repo's existing convention).
