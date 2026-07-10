# my-device: all project work happens over SSH

This machine is only where the agent runs. The real development environment —
the project files, toolchain, tests, git, data — is the user's own machine,
reachable as `my-device` through a reverse SSH tunnel.

Project directory on my-device: the user will normally say which project to
work on — use that path in every `cd`. If they did not say, the configured
default is `CLAUDE_LOCAL_DIR` in `~/.config/claude-local/env`. When no
directory is clear, ask the user instead of guessing, and record the answer
by updating `CLAUDE_LOCAL_DIR` in that file so later sessions start with
the right default (touching that file here is fine — it is config, not
project data). Note that `ssh my-device` lands in the home directory, so
every command must `cd` explicitly.

## Hard rules

- Everything that touches the project — build, test, lint, git, running any
  program, generating any data — happens ON MY-DEVICE through the Bash tool:
  `ssh my-device 'cd <project dir> && <command>'`
- This server is for lightweight work only: drafting small scripts and
  patches, and network tools (WebSearch / WebFetch), which run here and are
  fine to use directly. Do not generate project data on this machine and do
  not install project toolchains or dependencies here.
- Use `~/tmp/` on this server for scratch files.
- Read, Edit, Write, Glob, Grep, and NotebookEdit operate on THIS machine's
  filesystem, which does not contain the project. Never point them at
  project paths directly — anything they read is wrong and anything they
  write is lost. To use them on project files, set up one of the two
  arrangements below first.

## Using file tools on project files

1. scp round-trip — copy the file to `~/tmp/`, edit it there with the file
   tools, copy it back:

       scp my-device:'<project dir>/src/main.py' ~/tmp/
       (Read / Edit ~/tmp/main.py)
       scp ~/tmp/main.py my-device:'<project dir>/src/main.py'

   If commands on my-device may have changed the file in between, re-copy
   it before editing again.

2. sshfs mount — mount the project directory onto this server (needs sshfs
   installed here); file tools then see the real project files at the
   mountpoint:

       mkdir -p ~/claude-local-project
       sshfs my-device:'<project dir>' ~/claude-local-project \
             -o reconnect,ServerAliveInterval=15,ServerAliveCountMax=3
       fusermount -u ~/claude-local-project     # unmount when done

   Still run commands through `ssh my-device`, not against the mount.

## Patterns

Explore and read:

    ssh my-device 'cd <project dir> && ls -la'
    ssh my-device 'cd <project dir> && sed -n "1,120p" src/main.py'
    ssh my-device 'cd <project dir> && grep -rn "pattern" src/'

Run, test, git:

    ssh my-device 'cd <project dir> && make test'
    ssh my-device 'cd <project dir> && git status'

Write a script here, ship it, run it over there:

    (Write ~/tmp/fix.py with the file tools)
    scp ~/tmp/fix.py my-device:'<project dir>/'
    ssh my-device 'cd <project dir> && python fix.py'

Small edits — prefer a patch over rewriting the file:

    ssh my-device 'cd <project dir> && git apply' <<'EOF'
    diff --git a/src/main.py b/src/main.py
    ...
    EOF

When quoting through two shells gets hairy, pipe a whole script instead
(quoted delimiters stop local expansion):

    ssh my-device 'bash -s' <<'REMOTE'
    cd <project dir>
    cat > src/config.py <<'EOF'
    ...new file content...
    EOF
    REMOTE

## When ssh my-device fails

- `Connection refused`: the reverse tunnel is down. Tell the user to start
  `ssh -N remote-claude` on their machine (or check its autostart). Nothing
  on this server can fix it — do not retry endlessly or work around it by
  editing files here.
- Host key mismatch: stop and tell the user; the machine behind the tunnel
  may have changed.
