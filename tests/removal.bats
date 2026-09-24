#!/usr/bin/env bats
# Removal paths: packaging/scripts/postrm (package removal, must leave the host
# firewall alone) and uninstall.sh (full uninstall, resets UFW).

load lib/agent-test

setup() {
  setup_scratch
  mkdir -p "$ROOT/etc/lsh-agent" "$ROOT/usr/local/bin"
  stub systemctl
}

postrm() {
  run env PATH="$(stub_path)" ROOT="$ROOT" sh -c "$(rooted < "$POSTRM")" postrm "$@"
}

# --- postrm -----------------------------------------------------------------

@test "postrm never touches UFW (removing the package keeps the host firewall)" {
  assert_lacks_code_re "$POSTRM" '(^|[[:space:];])ufw[[:space:]]'
}

@test "postrm remove drops the compatibility link postinst created" {
  ln -s /usr/bin/lsh-agent "$ROOT/usr/local/bin/lsh-agent"
  postrm remove
  [ "$status" -eq 0 ]
  [ ! -L "$ROOT/usr/local/bin/lsh-agent" ]
}

@test "postrm remove keeps a /usr/local/bin/lsh-agent that is not our link" {
  ln -s /opt/other/lsh-agent "$ROOT/usr/local/bin/lsh-agent"
  postrm remove
  [ -L "$ROOT/usr/local/bin/lsh-agent" ]
}

@test "postrm purge removes the env file (not a conffile)" {
  printf 'PROJECT_ID=p\n' > "$ROOT/etc/lsh-agent/env"
  postrm purge
  [ "$status" -eq 0 ]
  [ ! -e "$ROOT/etc/lsh-agent/env" ]
}

@test "postrm upgrade leaves the env file alone" {
  printf 'PROJECT_ID=p\n' > "$ROOT/etc/lsh-agent/env"
  postrm upgrade
  [ -f "$ROOT/etc/lsh-agent/env" ]
}

# --- uninstall.sh (contract: needs root and a real host to run) -------------

@test "uninstall resets and disables UFW only when ufw exists" {
  run grep -n -A3 'if command -v ufw' "$UNINSTALL_SH"
  [[ "$output" == *"ufw --force reset"* ]]
  [[ "$output" == *"ufw disable"* ]]
}

@test "uninstall keeps lsh-agent-netfilter-restore (it restores the metadata DNAT)" {
  # netfilter-persistent is gone once ufw replaced it; without this unit the
  # metadata redirect in /etc/iptables/rules.v4 is lost on the next boot.
  assert_lacks_code_re "$UNINSTALL_SH" 'lsh-agent-netfilter-restore'
}

@test "uninstall stops before deleting files or resetting UFW when apt purge fails" {
  purge_fail_exit=$(grep -n 'could not remove the lsh-agent package' "$UNINSTALL_SH" | cut -d: -f1)
  first_rm=$(grep -n '^rm -f /etc/systemd/system/lsh-agent.service' "$UNINSTALL_SH" | cut -d: -f1)
  first_reset=$(grep -n 'ufw --force reset' "$UNINSTALL_SH" | cut -d: -f1)
  [ -n "$purge_fail_exit" ]
  [ -n "$first_rm" ]
  [ -n "$first_reset" ]
  [ "$purge_fail_exit" -lt "$first_rm" ]
  [ "$purge_fail_exit" -lt "$first_reset" ]
  sed -n "${purge_fail_exit},$((purge_fail_exit + 1))p" "$UNINSTALL_SH" | grep -q 'exit 1'
}

@test "uninstall refuses to run without /etc/lsh-agent/env" {
  [ -e /etc/lsh-agent/env ] && skip "this host has a real agent config"
  run bash "$UNINSTALL_SH"
  [ "$status" -eq 1 ]
  [[ "$output" == *"env file not found"* ]]
}
