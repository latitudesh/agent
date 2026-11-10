#!/usr/bin/env bats
# Removal paths: packaging/scripts/postrm (package removal, must leave the host
# firewall alone) and uninstall.sh (full uninstall, resets UFW).

load lib/agent-test

setup() {
  setup_scratch
  mkdir -p "$ROOT/etc/lsh-agent" "$ROOT/usr/local/bin"
  stub systemctl
  # postrm probes /usr/bin/deb-systemd-helper by absolute path; point that
  # probe at the stub so a runner's real helper is never executed.
  stub deb-systemd-helper
}

postrm() {
  run env PATH="$(stub_path)" ROOT="$ROOT" \
    sh -c "$(rooted < "$POSTRM" | sed "s#/usr/bin/deb-systemd-helper#$STUB_BIN/deb-systemd-helper#g")" postrm "$@"
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

@test "postrm remove masks the unit; purge purges and unmasks it" {
  postrm remove
  [ "$status" -eq 0 ]
  called "deb-systemd-helper mask lsh-agent.service"
  : > "$CALLS"
  postrm purge
  [ "$status" -eq 0 ]
  called "deb-systemd-helper purge lsh-agent.service"
  called "deb-systemd-helper unmask lsh-agent.service"
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

@test "uninstall stops before deleting files or resetting UFW when apt purge or dnf remove fails" {
  first_rm=$(grep -n '^rm -f /etc/systemd/system/lsh-agent.service' "$UNINSTALL_SH" | cut -d: -f1)
  first_reset=$(grep -n 'ufw --force reset' "$UNINSTALL_SH" | cut -d: -f1)
  [ -n "$first_rm" ]
  [ -n "$first_reset" ]
  # One failure exit per package manager (apt, dnf).
  [ "$(grep -c 'could not remove the lsh-agent package' "$UNINSTALL_SH")" -eq 2 ]
  for fail_exit in $(grep -n 'could not remove the lsh-agent package' "$UNINSTALL_SH" | cut -d: -f1); do
    [ "$fail_exit" -lt "$first_rm" ]
    [ "$fail_exit" -lt "$first_reset" ]
    sed -n "${fail_exit},$((fail_exit + 1))p" "$UNINSTALL_SH" | grep -q 'exit 1'
  done
}

@test "uninstall keeps ufw installed on the RHEL family (no dependency clean-up)" {
  assert_contains "$UNINSTALL_SH" 'dnf -y --setopt=clean_requirements_on_remove=False remove lsh-agent'
}

@test "uninstall as non-root refuses before reading anything" {
  [ "$(id -u)" = 0 ] && skip "running as root: uninstall.sh would really uninstall"
  run bash "$UNINSTALL_SH"
  [ "$status" -eq 1 ]
  [[ "$output" == *"Please run as root"* ]]
}

@test "uninstall refuses to run without /etc/lsh-agent/env" {
  run grep -n -A4 'if \[ -f /etc/lsh-agent/env \]' "$UNINSTALL_SH"
  [[ "$output" == *"env file not found"* ]]
  [[ "$output" == *"exit 1"* ]]
}

@test "uninstall never sources the env file (it is data, not a script)" {
  assert_lacks_code_re "$UNINSTALL_SH" '(^|[[:space:];])(source|\.)[[:space:]]+[^[:space:]]*lsh-agent/env'
}

@test "uninstall checks for root before reading the env file" {
  root_check=$(grep -n 'if \[ "\$EUID" -ne 0 \]' "$UNINSTALL_SH" | head -n 1 | cut -d: -f1)
  first_read=$(grep -n 'lsh-agent/env' "$UNINSTALL_SH" | grep -v '^[0-9]*:#' | head -n 1 | cut -d: -f1)
  [ -n "$root_check" ]
  [ -n "$first_read" ]
  [ "$root_check" -lt "$first_read" ]
}

@test "env_value reads a crafted env file without executing it" {
  eval "$(extract_fn "$UNINSTALL_SH" env_value)"
  local env="$ROOT/env" marker="$BATS_TEST_TMPDIR/ran"
  printf 'FIREWALL_ID=fw_1\nPROJECT_ID=p1;touch %s\nPUBLIC_IP=$(touch %s)\nX=`touch %s`\n' \
    "$marker" "$marker" "$marker" > "$env"
  run env_value "$env" PROJECT_ID
  [ "$status" -eq 0 ]
  [ "$output" = "p1;touch $marker" ]
  run env_value "$env" PUBLIC_IP
  [ "$output" = "\$(touch $marker)" ]
  [ ! -e "$marker" ]
}

@test "env_value returns the last value of a key and nothing for a missing key" {
  eval "$(extract_fn "$UNINSTALL_SH" env_value)"
  printf 'PROJECT_ID=old\nPROJECT_ID=proj_1\n' > "$ROOT/env"
  run env_value "$ROOT/env" PROJECT_ID
  [ "$output" = proj_1 ]
  run env_value "$ROOT/env" FIREWALL_ID
  [ -z "$output" ]
}
