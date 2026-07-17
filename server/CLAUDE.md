# my-device: the project lives on the user's machine

You run on this server, but the real development environment — project
files, toolchain, tests, git, data — is the user's own machine, reachable
as `my-device` through a reverse SSH tunnel.

Work EXACTLY as you would on a local project: explore first, read the
relevant files, then plan, then edit, then run and verify. Being remote
changes none of that order. Only two mechanics differ:

1. Shell commands run on my-device through the Bash tool:
   `ssh my-device 'cd <project dir> && <command>'`
   Every ssh lands in the home directory — cd explicitly in each command.
2. The file tools (Read, Edit, Write, Glob, Grep, NotebookEdit) are the
   one exception: they operate on THIS server's filesystem, which does not
   contain the project. Never point them at project paths — use the scp
   round-trip below.

The tell-tale failure to avoid: drafting project code under `~/tmp/`
before you have even listed the project directory. If you would have
explored first on a local path, explore first here — over ssh.

## Boundaries

- Build, test, lint, git, running programs, generating data: on my-device
  only. Do not install project toolchains or dependencies on this server,
  and do not keep project data here.
- Fine on this server: network tools (WebSearch / WebFetch) and scratch
  files under `~/tmp/`.

## File tools: the scp round-trip

Edit an existing project file — copy it here, edit, copy it back:

    scp my-device:'<project dir>/src/main.py' ~/tmp/
    (Read / Edit ~/tmp/main.py)
    scp ~/tmp/main.py my-device:'<project dir>/src/main.py'

Create a new project file — Write it under `~/tmp/`, then scp it in:

    (Write ~/tmp/snake.py)
    scp ~/tmp/snake.py my-device:'<project dir>/src/snake.py'

If commands on my-device may have changed a file in between, re-copy it
before editing again.

## Durable facts: ~/.config/remote-claude/facts.json

Your persistent memory about the user's machine. Read it at the START of
every session; update it (create it if missing) whenever you learn a
durable fact.

    {
      "machine": { "os": "unknown", "ssh_shell": "unknown" },
      "projects": {
        "foo": { "path": "~/projects/foo", "desc": "Rust CLI tool" }
      }
    }

- machine: while "unknown", detect on first contact (`ssh my-device
  uname -s`; if that errors it is likely Windows — try
  `ssh my-device cmd /c ver`) and record the result.
- projects: one entry per project — path on my-device plus a one-line
  description.

Which project to work on: the user normally says; otherwise check the
facts file; when still unclear, ask — never guess. Record the answer.

## Patterns

These assume a POSIX shell on my-device. If the facts file says Windows
(cmd / PowerShell), translate accordingly and record working equivalents
in the facts file.

Explore and read:

    ssh my-device 'cd <project dir> && ls -la'
    ssh my-device 'cd <project dir> && sed -n "1,120p" src/main.py'
    ssh my-device 'cd <project dir> && grep -rn "pattern" src/'

Run, test, git:

    ssh my-device 'cd <project dir> && make test'
    ssh my-device 'cd <project dir> && git status'

Small edits — a patch beats rewriting the file:

    ssh my-device 'cd <project dir> && git apply' <<'EOF'
    diff --git a/src/main.py b/src/main.py
    ...
    EOF

When quoting through two shells gets hairy, pipe a whole script instead
(quoted delimiters stop local expansion):

    ssh my-device 'bash -s' <<'REMOTE'
    cd <project dir>
    ...
    REMOTE

## When ssh my-device fails

- `Connection refused`: the reverse tunnel is down. Tell the user to reconnect
  to the server (VSCode Remote-SSH, or `ssh remote-claude`) — the tunnel rides
  on that connection. Nothing on this server can fix it — do not retry
  endlessly or work around it by editing files here.
- Host key mismatch: stop and tell the user; the machine behind the tunnel
  may have changed.
