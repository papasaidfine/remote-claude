# my-device: all project work happens over SSH

This machine is only where the agent runs. The real development environment —
the project files, toolchain, tests, git, data — is the user's own machine,
reachable as `my-device` through a reverse SSH tunnel.

## Durable facts: read them first, keep them updated

A `## my-device facts` section elsewhere in `~/.claude/CLAUDE.md` (outside
the managed instructions block) is your persistent memory about the user's
machine: its OS and default ssh shell, each project's path with a one-line
description, and where projects get mounted on this server. Trust it before
probing — do not rediscover per session what is already recorded there.
When you learn such a durable fact the hard way, update the section with
the Edit tool (create it if missing; editing this memory file is fine —
the file-tool ban is about project files). Runtime state does NOT belong
there: whether the tunnel is up or a mount is alive must be checked live,
never assumed from memory.

Project directory on my-device: the user will normally say which project to
work on — use that path in every `cd`. If they did not say, check the
my-device facts section. When no directory is clear, ask the user instead
of guessing, and record the answer in the facts section so later sessions
start with the right default. Note that `ssh my-device` lands in the home
directory, so every command must `cd` explicitly.

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

       mkdir -p ~/my-device-project
       sshfs my-device:'<project dir>' ~/my-device-project \
             -o reconnect,ServerAliveInterval=15,ServerAliveCountMax=3
       fusermount -u ~/my-device-project     # unmount when done

   A dropped tunnel can leave a zombie mount ("Transport endpoint is not
   connected"). Before trusting an existing mount, check it with
   `mountpoint ~/my-device-project` or a quick `ls`; if it is dead,
   `fusermount -u` and remount. Record the project → mountpoint mapping
   in the facts section.

   Still run commands through `ssh my-device`, not against the mount.

## Patterns

These assume a POSIX shell answers on my-device. If the facts section says
it is Windows (cmd/PowerShell), translate the commands accordingly — and
record working equivalents in the facts section as you find them.

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
