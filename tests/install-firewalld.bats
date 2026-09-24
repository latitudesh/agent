#!/usr/bin/env bats
# install.sh: firewalld -> UFW hand-over on the RHEL family (disable_firewalld,
# restore_firewalld). The property that matters: the host is never left with
# two netfilter managers, and a failed switch never leaves it with none.

load lib/agent-test

setup() {
  setup_scratch
  FUNCS="$(extract_fn "$INSTALL_SH" restore_firewalld disable_firewalld)"
  # systemctl stub backed by state files: $ROOT/enabled, $ROOT/active.
  # $ROOT/stuck makes `disable --now` a no-op (firewalld refuses to go).
  stub_script systemctl '
case "$*" in
  "is-enabled --quiet firewalld") [ -e "$ROOT/enabled" ] ;;
  "is-active --quiet firewalld")  [ -e "$ROOT/active" ] ;;
  "disable --now firewalld")      [ -e "$ROOT/stuck" ] || rm -f "$ROOT/enabled" "$ROOT/active" ;;
  "enable firewalld")             touch "$ROOT/enabled" ;;
  "start firewalld")              touch "$ROOT/active" ;;
esac'
}

# fw <family> <call...>: run the extracted functions, print the WAS_* flags.
fw() {
  local family="$1"; shift
  run bash -c '
    PATH="$1"; OS_FAMILY="$2"; FIREWALLD_WAS_ENABLED=0; FIREWALLD_WAS_ACTIVE=0
    eval "$3"; shift 3
    for c in "$@"; do $c || exit $?; done
    echo "enabled=$FIREWALLD_WAS_ENABLED active=$FIREWALLD_WAS_ACTIVE"
  ' _ "$(stub_path)" "$family" "$FUNCS" "$@"
}

@test "Debian family: firewalld is never touched" {
  touch "$ROOT/enabled" "$ROOT/active"
  fw debian disable_firewalld
  [ "$status" -eq 0 ]
  [ ! -s "$CALLS" ]
}

@test "RHEL without firewalld: nothing is disabled" {
  fw rhel disable_firewalld
  [ "$status" -eq 0 ]
  [[ "$output" == *"enabled=0 active=0"* ]]
  not_called_with "systemctl disable"
}

@test "RHEL with firewalld enabled+active: it is disabled and both states remembered" {
  touch "$ROOT/enabled" "$ROOT/active"
  fw rhel disable_firewalld
  [ "$status" -eq 0 ]
  [[ "$output" == *"enabled=1 active=1"* ]]
  called "systemctl disable --now firewalld"
  [ ! -e "$ROOT/enabled" ]
  [ ! -e "$ROOT/active" ]
}

@test "enabled-but-stopped firewalld is still disabled (it would return on reboot)" {
  touch "$ROOT/enabled"
  fw rhel disable_firewalld
  [ "$status" -eq 0 ]
  [[ "$output" == *"enabled=1 active=0"* ]]
  called "systemctl disable --now firewalld"
}

@test "firewalld that will not go away aborts the install" {
  touch "$ROOT/enabled" "$ROOT/active" "$ROOT/stuck"
  fw rhel disable_firewalld
  [ "$status" -eq 1 ]
  [[ "$output" == *"could not disable firewalld"* ]]
}

@test "restore after a failed UFW switch brings firewalld back as it was" {
  touch "$ROOT/enabled" "$ROOT/active"
  fw rhel disable_firewalld restore_firewalld
  [ "$status" -eq 0 ]
  [ -e "$ROOT/enabled" ]
  [ -e "$ROOT/active" ]
  [[ "$output" == *"firewalld restored"* ]]
}

@test "restore only re-enables what was on before" {
  touch "$ROOT/enabled"
  fw rhel disable_firewalld restore_firewalld
  [ -e "$ROOT/enabled" ]
  [ ! -e "$ROOT/active" ]
  not_called_with "systemctl start"
}

@test "restore is silent when firewalld was never there" {
  fw rhel restore_firewalld
  [ "$status" -eq 0 ]
  [[ "$output" != *"restored"* ]]
  [ ! -s "$CALLS" ]
}

@test "the restore is armed only around 'ufw --force enable'" {
  # trap set right before enabling UFW, cleared right after: a later failure
  # (package install, config) must not flip firewalld back on under a live UFW.
  run grep -n -A2 'trap restore_firewalld EXIT' "$INSTALL_SH"
  [[ "$output" == *"ufw --force enable"* ]]
  [[ "$output" == *"trap - EXIT"* ]]
}
