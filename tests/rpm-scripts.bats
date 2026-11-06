#!/usr/bin/env bats
# packaging/scripts/rpm/{post,preun,postun}, run for real against a throwaway
# root ($ROOT) with a stub systemctl. RPM passes a package count, not an action
# name: post 1 = first install, 2 = upgrade; preun/postun 0 = removal.
# The behaviour mirrors the .deb scripts (tests/postinst.bats, removal.bats).

load lib/agent-test

setup() {
  setup_scratch
  mkdir -p "$ROOT/etc/systemd/system/multi-user.target.wants" "$ROOT/etc/lsh-agent" "$ROOT/usr/local/bin"
  stub systemctl
}

rpm_script() {
  local script="$1"
  shift
  run env PATH="$(stub_path)" ROOT="$ROOT" sh -c "$(rooted < "$script")" rpm-script "$@"
}

# --- post --------------------------------------------------------------------

@test "post migrates install.sh hosts exactly like the .deb postinst" {
  deb="$(extract_fn "$POSTINST" migrate_from_install_sh)"
  rpm="$(extract_fn "$RPM_POST" migrate_from_install_sh)"
  [ "$deb" = "$rpm" ]
}

@test "post on first install: legacy install.sh unit removed, link created, unit enabled" {
  legacy="$ROOT/etc/systemd/system/lsh-agent.service"
  printf '[Service]\nExecStart=/usr/local/bin/lsh-agent -config /etc/lsh-agent/config.yaml\n' > "$legacy"
  ln -s "$legacy" "$ROOT/etc/systemd/system/multi-user.target.wants/lsh-agent.service"
  printf 'old binary\n' > "$ROOT/usr/local/bin/lsh-agent"
  rpm_script "$RPM_POST" 1
  [ "$status" -eq 0 ]
  [ ! -e "$legacy" ]
  [ ! -L "$ROOT/etc/systemd/system/multi-user.target.wants/lsh-agent.service" ]
  [ "$(readlink "$ROOT/usr/local/bin/lsh-agent")" = /usr/bin/lsh-agent ]
  called "systemctl enable lsh-agent.service"
}

@test "post on upgrade keeps the admin's enable/disable choice" {
  rpm_script "$RPM_POST" 2
  [ "$status" -eq 0 ]
  not_called_with "systemctl enable"
}

@test "post restarts an enabled agent onto the new binary" {
  [ -d /run/systemd/system ] || skip "no systemd on this runner"
  rpm_script "$RPM_POST" 2
  called "systemctl restart lsh-agent.service"
}

@test "post leaves an agent the admin disabled stopped" {
  [ -d /run/systemd/system ] || skip "no systemd on this runner"
  stub_script systemctl '[ "$1" = is-enabled ] && exit 1; exit 0'
  rpm_script "$RPM_POST" 2
  [ "$status" -eq 0 ]
  not_called_with "systemctl restart"
}

# --- preun -------------------------------------------------------------------

@test "preun on removal disables the unit" {
  rpm_script "$RPM_PREUN" 0
  [ "$status" -eq 0 ]
  called "systemctl disable lsh-agent.service"
}

@test "preun on upgrade leaves the agent running and enabled" {
  rpm_script "$RPM_PREUN" 1
  [ "$status" -eq 0 ]
  not_called_with "systemctl stop"
  not_called_with "systemctl disable"
}

# --- postun ------------------------------------------------------------------

@test "postun never touches UFW (removing the package keeps the host firewall)" {
  assert_lacks_code_re "$RPM_POSTUN" '(^|[[:space:];])ufw[[:space:]]'
  assert_lacks_code_re "$RPM_PREUN" '(^|[[:space:];])ufw[[:space:]]'
}

@test "postun on removal drops the compatibility link post created" {
  ln -s /usr/bin/lsh-agent "$ROOT/usr/local/bin/lsh-agent"
  rpm_script "$RPM_POSTUN" 0
  [ "$status" -eq 0 ]
  [ ! -L "$ROOT/usr/local/bin/lsh-agent" ]
}

@test "postun on removal keeps a /usr/local/bin/lsh-agent that is not our link" {
  ln -s /opt/other/lsh-agent "$ROOT/usr/local/bin/lsh-agent"
  rpm_script "$RPM_POSTUN" 0
  [ -L "$ROOT/usr/local/bin/lsh-agent" ]
}

@test "postun on upgrade keeps the compatibility link" {
  ln -s /usr/bin/lsh-agent "$ROOT/usr/local/bin/lsh-agent"
  rpm_script "$RPM_POSTUN" 1
  [ -L "$ROOT/usr/local/bin/lsh-agent" ]
}

@test "postun keeps the env file (RPM has no purge; uninstall.sh removes it)" {
  printf 'PROJECT_ID=p\n' > "$ROOT/etc/lsh-agent/env"
  rpm_script "$RPM_POSTUN" 0
  [ -f "$ROOT/etc/lsh-agent/env" ]
}
