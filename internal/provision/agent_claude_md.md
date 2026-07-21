# Working on the user's device over a reverse tunnel

You run on this server, but the real development environment — project files,
toolchain, tests, git, data — lives on the user's own machine, reached over a
reverse SSH tunnel.

**Which machine: `$LC_CLIENT_NAME`.** A user may connect from more than one
device; the device for THIS session is named in the `LC_CLIENT_NAME`
environment variable and is reachable as `ssh "$LC_CLIENT_NAME"`. Always use
that variable — never hardcode a device name.

Run `echo "$LC_CLIENT_NAME"` at the start of a session:
- Prints a name → that is your device; use `ssh "$LC_CLIENT_NAME" …`.
- EMPTY → the device name was not forwarded (the ssh session didn't carry
  `LC_CLIENT_NAME`). Stop and tell the user to reconnect through the
  remote-claude app, and to make sure the server accepts `LC_*` env
  (`AcceptEnv LANG LC_*` in sshd_config). Do NOT guess a device.

Work EXACTLY as on a local project: explore first, read the relevant files,
then plan, edit, run, verify. Being remote changes only the mechanics:

1. Shell commands run on the device through the Bash tool:
   `ssh "$LC_CLIENT_NAME" 'cd <project dir> && <command>'`
   Every ssh lands in the home directory — cd explicitly in each command.
2. The file tools (Read, Edit, Write, Glob, Grep, NotebookEdit) operate on
   THIS server's filesystem, which does NOT contain the project. Never point
   them at project paths — use the scp round-trip below.

The tell-tale failure to avoid: drafting project code under `~/tmp/` before you
have even listed the project directory. If you would explore first locally,
explore first here — over ssh.

## Boundaries

- Build, test, lint, git, running programs, generating data: on the device
  only. Do not install project toolchains or keep project data on this server.
- Fine on this server: WebSearch / WebFetch and scratch files under `~/tmp/`.

## File tools: the scp round-trip

Edit an existing project file — copy it here, edit, copy it back:

    scp "$LC_CLIENT_NAME":'<project dir>/src/main.py' ~/tmp/
    (Read / Edit ~/tmp/main.py)
    scp ~/tmp/main.py "$LC_CLIENT_NAME":'<project dir>/src/main.py'

Create a new project file — Write it under `~/tmp/`, then scp it in.

## Durable facts: ~/.config/remote-claude/facts/$LC_CLIENT_NAME.json

Per-device memory — a user's laptop and desktop keep separate files. Read your
device's file at the START of every session; create/update it (it may not exist
yet) whenever you learn a durable fact.

    {
      "machine": { "os": "unknown", "ssh_shell": "unknown" },
      "projects": { "foo": { "path": "~/projects/foo", "desc": "Rust CLI" } }
    }

- machine: while "unknown", detect on first contact (`ssh "$LC_CLIENT_NAME"
  uname -s`; if that errors it is likely Windows — try `ssh "$LC_CLIENT_NAME"
  cmd /c ver`) and record the result.
- projects: one entry per project on that device.

Which project to work on: the user normally says; otherwise check the facts
file; when still unclear, ask — never guess. Record the answer.

## Patterns

These assume a POSIX shell on the device. If the facts file says Windows (cmd /
PowerShell), translate accordingly and record working equivalents there.

    ssh "$LC_CLIENT_NAME" 'cd <project dir> && ls -la'
    ssh "$LC_CLIENT_NAME" 'cd <project dir> && grep -rn "pattern" src/'
    ssh "$LC_CLIENT_NAME" 'cd <project dir> && make test'
    ssh "$LC_CLIENT_NAME" 'cd <project dir> && git status'

Small edits — a patch beats rewriting the file:

    ssh "$LC_CLIENT_NAME" 'cd <project dir> && git apply' <<'PATCH'
    diff --git a/src/main.py b/src/main.py
    ...
    PATCH

When quoting through two shells gets hairy, pipe a whole script instead:

    ssh "$LC_CLIENT_NAME" 'bash -s' <<'REMOTE'
    cd <project dir>
    ...
    REMOTE

## When ssh "$LC_CLIENT_NAME" fails

- `Connection refused`: that device's reverse tunnel is down. Tell the user to
  make sure the remote-claude app is running on `$LC_CLIENT_NAME` (the app holds
  the tunnel up). Nothing on this server can fix it — do not retry endlessly or
  work around it by editing files here.
- Host key mismatch: stop and tell the user; the machine behind the tunnel may
  have changed.
