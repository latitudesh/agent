#!/usr/bin/env bats
# install.sh: the netfilter-persistent hand-over.
#
# On Debian 12/13 and Ubuntu 24.04+, installing ufw removes netfilter-persistent,
# which restored /etc/iptables/rules.v{4,6} (incl. the metadata DNAT) at boot.
# install.sh then writes lsh-agent-netfilter-restore.service to take over.
# It must be ordered before ufw: --noflush still applies the file's chain
# policies, so a policy-only rules.v6 restored after ufw resets its DROP to ACCEPT.

load lib/agent-test

setup() {
  setup_scratch
  UNIT_FILE="$BATS_TEST_TMPDIR/lsh-agent-netfilter-restore.service"
  sed -n "/lsh-agent-netfilter-restore.service << 'EOF'\$/,/^EOF\$/{//!p}" "$INSTALL_SH" > "$UNIT_FILE"
}

@test "install.sh writes a netfilter-restore unit" {
  [ -s "$UNIT_FILE" ]
}

@test "the unit is ordered Before=ufw.service" {
  grep -qE '^Before=(.*[[:space:]])?ufw\.service([[:space:]]|$)' "$UNIT_FILE"
}

@test "the unit keeps netfilter-persistent's early-boot ordering" {
  grep -qx 'DefaultDependencies=no' "$UNIT_FILE"
  grep -qE '^Before=.*network-pre\.target' "$UNIT_FILE"
  grep -qx 'WantedBy=multi-user.target' "$UNIT_FILE"
}

@test "both rule files are restored with --noflush (add, never wipe ufw's rules)" {
  grep -qE 'iptables-restore --noflush .*/etc/iptables/rules\.v4' "$UNIT_FILE"
  grep -qE 'ip6tables-restore --noflush .*/etc/iptables/rules\.v6' "$UNIT_FILE"
}

@test "the unit is only written when netfilter-persistent was really removed" {
  assert_contains "$INSTALL_SH" 'if [ "$netfilter_persistent_was_installed" = 1 ] && ! netfilter_persistent_installed; then'
}

@test "netfilter_persistent_installed reads dpkg's status" {
  fn="$(extract_fn "$INSTALL_SH" netfilter_persistent_installed)"
  stub dpkg-query 0 "install ok installed"
  run bash -c 'PATH="$1"; eval "$2"; netfilter_persistent_installed' _ "$(stub_path)" "$fn"
  [ "$status" -eq 0 ]
  stub dpkg-query 0 "deinstall ok config-files"
  run bash -c 'PATH="$1"; eval "$2"; netfilter_persistent_installed' _ "$(stub_path)" "$fn"
  [ "$status" -ne 0 ]
  stub dpkg-query 1
  run bash -c 'PATH="$1"; eval "$2"; netfilter_persistent_installed' _ "$(stub_path)" "$fn"
  [ "$status" -ne 0 ]
}
