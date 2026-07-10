# my-device: all project work happens over SSH

This machine is only where the agent runs. The real development environment —
the project files, toolchain, tests, git — is the user's own machine,
reachable as `my-device` through a reverse SSH tunnel.

Project directory on my-device: the user will normally say which project to
work on — use that path in every `cd`. If they did not say, the configured
default is `CLAUDE_LOCAL_DIR` in `~/.config/claude-local/env` (reading that
file here is fine — it is config, not project data). When no directory is
clear, ask the user instead of guessing. Note that `ssh my-device` lands in
the home directory, so every command must `cd` explicitly.

## Hard rules

- Run every project operation through SSH from the Bash tool:
  `ssh my-device 'cd <project dir> && <command>'`
- NEVER use Read, Edit, Write, Glob, Grep, or NotebookEdit on project files.
  Those tools operate on THIS machine's filesystem, which does not contain
  the project — anything they read is wrong and anything they write is lost.
- Do not install project toolchains or dependencies on this machine. Build,
  test, lint, and use git on my-device.

## Patterns

Explore and read:

    ssh my-device 'cd <project dir> && ls -la'
    ssh my-device 'cd <project dir> && sed -n "1,120p" src/main.py'
    ssh my-device 'cd <project dir> && grep -rn "pattern" src/'

Run, test, git:

    ssh my-device 'cd <project dir> && make test'
    ssh my-device 'cd <project dir> && git status'

Write a whole file (pipe a script to bash; quoted delimiters stop local
expansion):

    ssh my-device 'bash -s' <<'REMOTE'
    cd <project dir>
    cat > src/config.py <<'EOF'
    ...new file content...
    EOF
    REMOTE

Small edits — prefer a patch over rewriting the file:

    ssh my-device 'cd <project dir> && git apply' <<'EOF'
    diff --git a/src/main.py b/src/main.py
    ...
    EOF

## When ssh my-device fails

- `Connection refused`: the reverse tunnel is down. Tell the user to start
  `ssh -N remote-claude` on their machine (or check its autostart). Nothing
  on this server can fix it — do not retry endlessly or work around it by
  editing files here.
- Host key mismatch: stop and tell the user; the machine behind the tunnel
  may have changed.
