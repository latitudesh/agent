#!/usr/bin/env bash
# shellcheck disable=SC2034  # the path variables below are read by the suites that load this file
# tests/lib/agent-test.bash
#
# Shared helpers for the agent's Bats suites. Every suite loads this file.
#
# These tests never run install.sh/uninstall.sh/the package scripts for real:
# those need root and change the host (ufw, systemd, apt). Instead they either
# grep the source (contract tests) or extract single functions and run them
# against stub commands and a throwaway root directory (unit tests).
# The Docker-based end-to-end runs live in scripts/test-install-sh.sh and
# scripts/test-apt-install.sh.

REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/.." && pwd)"
INSTALL_SH="$REPO_ROOT/install.sh"
UNINSTALL_SH="$REPO_ROOT/uninstall.sh"
POSTINST="$REPO_ROOT/packaging/scripts/postinst"
POSTRM="$REPO_ROOT/packaging/scripts/postrm"

# Per-test scratch: $ROOT is a fake filesystem root, $STUB_BIN holds stub
# commands, $CALLS logs every stub invocation ("name args...", one per line).
setup_scratch() {
  ROOT="$BATS_TEST_TMPDIR/root"
  STUB_BIN="$BATS_TEST_TMPDIR/bin"
  CALLS="$BATS_TEST_TMPDIR/calls.log"
  mkdir -p "$ROOT" "$STUB_BIN"
  : > "$CALLS"
  export ROOT STUB_BIN CALLS
}

# Print the source of one or more top-level shell functions from a file.
# Fails when any of them is missing, so a rename breaks the test loudly.
extract_fn() {
  local file="$1" name out=""
  shift
  for name in "$@"; do
    local body
    body="$(sed -n "/^${name}() {\$/,/^}\$/p" "$file")"
    if [ -z "$body" ]; then
      echo "extract_fn: ${name}() not found in ${file}" >&2
      return 1
    fi
    out+="$body"$'\n'
  done
  printf '%s' "$out"
}

# Rewrite absolute host paths in script source so it runs against $ROOT.
# Left alone: symlink targets compared as plain strings (/usr/bin/lsh-agent)
# and unit-file content matched by grep (ExecStart=/usr/local/bin/...).
rooted() {
  sed -e 's#ExecStart=/#ExecStart=@KEEP@/#g' \
      -e 's#\([ ="]\)/etc/#\1$ROOT/etc/#g' \
      -e 's#\([ ="]\)/usr/local/bin/#\1$ROOT/usr/local/bin/#g' \
      -e 's#ExecStart=@KEEP@/#ExecStart=/#g'
}

# stub <name> [exit-code] [stdout]: a command that logs its args to $CALLS.
stub() {
  local name="$1" rc="${2:-0}" out="${3:-}"
  {
    printf '#!/bin/sh\n'
    printf 'echo "%s $*" >> "%s"\n' "$name" "$CALLS"
    [ -n "$out" ] && printf 'printf "%%s\\n" %q\n' "$out"
    printf 'exit %s\n' "$rc"
  } > "$STUB_BIN/$name"
  chmod +x "$STUB_BIN/$name"
}

# stub_script <name> <body>: a stub with custom sh logic (after the logging line).
stub_script() {
  local name="$1" body="$2"
  printf '#!/bin/sh\necho "%s $*" >> "%s"\n%s\n' "$name" "$CALLS" "$body" > "$STUB_BIN/$name"
  chmod +x "$STUB_BIN/$name"
}

# The stubs first, then only the base system: the runner's own ufw/systemctl
# must never be reached from a test.
stub_path() { printf '%s:/usr/bin:/bin' "$STUB_BIN"; }

called() { grep -qxF -- "$1" "$CALLS"; }
not_called_with() { ! grep -q -- "^$1" "$CALLS"; }

# Contract helpers (source greps; comment lines ignored where it matters).
assert_contains() {
  local file="$1" needle="$2"
  grep -qF -- "$needle" "$file" || { echo "expected in $file: $needle" >&2; return 1; }
}
assert_lacks_code_re() {
  local file="$1" re="$2" hit
  if hit="$(grep -nE -- "$re" "$file" | grep -vE '^[0-9]+:[[:space:]]*#')"; then
    echo "unexpected in $file (/$re/): $hit" >&2
    return 1
  fi
}
