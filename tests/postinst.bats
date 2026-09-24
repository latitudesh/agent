#!/usr/bin/env bats
# packaging/scripts/postinst, run for real against a throwaway root ($ROOT):
# /etc and /usr/local/bin are rewritten under it, deb-systemd-helper,
# deb-systemd-invoke and systemctl are stubs.

load lib/agent-test

setup() {
  setup_scratch
  mkdir -p "$ROOT/etc/systemd/system/multi-user.target.wants" "$ROOT/usr/local/bin"
  stub deb-systemd-helper
  stub deb-systemd-invoke
  stub systemctl
  NR_UNIT="$ROOT/etc/systemd/system/lsh-agent-netfilter-restore.service"
  # The unit exactly as install.sh writes it today, and a pre-fix copy.
  sed -n "/lsh-agent-netfilter-restore.service << 'EOF'\$/,/^EOF\$/{//!p}" "$INSTALL_SH" > "$BATS_TEST_TMPDIR/current.service"
  sed 's/^Before=network-pre.target shutdown.target ufw.service$/Before=network-pre.target shutdown.target/' \
    "$BATS_TEST_TMPDIR/current.service" > "$BATS_TEST_TMPDIR/old.service"
}

postinst() {
  run env PATH="$(stub_path)" ROOT="$ROOT" NETFILTER_RESTORE_UNIT="$NR_UNIT" \
    sh -c "$(rooted < "$POSTINST")" postinst "$@"
}

# --- netfilter-restore ordering (hosts set up by an older install.sh) -------

@test "the pre-fix unit fixture really lacks the ufw ordering" {
  run grep -q 'ufw.service' "$BATS_TEST_TMPDIR/old.service"
  [ "$status" -ne 0 ]
  grep -q '^Before=network-pre.target shutdown.target$' "$BATS_TEST_TMPDIR/old.service"
}

@test "no netfilter-restore unit: no drop-in" {
  postinst configure
  [ "$status" -eq 0 ]
  [ ! -e "$NR_UNIT.d" ]
}

@test "pre-fix unit: a Before=ufw.service drop-in is added" {
  cp "$BATS_TEST_TMPDIR/old.service" "$NR_UNIT"
  postinst configure
  [ "$status" -eq 0 ]
  grep -qx 'Before=ufw.service' "$NR_UNIT.d/10-before-ufw.conf"
  # The unit file itself is left untouched.
  cmp -s "$BATS_TEST_TMPDIR/old.service" "$NR_UNIT"
}

@test "already-fixed unit: no drop-in" {
  cp "$BATS_TEST_TMPDIR/current.service" "$NR_UNIT"
  postinst configure
  [ "$status" -eq 0 ]
  [ ! -e "$NR_UNIT.d" ]
}

@test "the drop-in is only added on configure" {
  cp "$BATS_TEST_TMPDIR/old.service" "$NR_UNIT"
  postinst abort-upgrade
  [ ! -e "$NR_UNIT.d" ]
}

# --- migration from an install.sh (source-built) setup ----------------------

@test "a legacy install.sh unit is removed so the packaged one runs" {
  legacy="$ROOT/etc/systemd/system/lsh-agent.service"
  printf '[Service]\nExecStart=/usr/local/bin/lsh-agent -config /etc/lsh-agent/config.yaml\n' > "$legacy"
  ln -s "$legacy" "$ROOT/etc/systemd/system/multi-user.target.wants/lsh-agent.service"
  postinst configure
  [ "$status" -eq 0 ]
  [ ! -e "$legacy" ]
  [ ! -L "$ROOT/etc/systemd/system/multi-user.target.wants/lsh-agent.service" ]
  called "deb-systemd-helper enable lsh-agent.service"
}

@test "an admin's own lsh-agent.service override is kept" {
  own="$ROOT/etc/systemd/system/lsh-agent.service"
  printf '[Service]\nExecStart=/usr/bin/lsh-agent -config /etc/lsh-agent/custom.yaml\n' > "$own"
  postinst configure
  [ -f "$own" ]
}

@test "a source-built /usr/local/bin/lsh-agent becomes a link to the packaged binary" {
  printf 'old binary\n' > "$ROOT/usr/local/bin/lsh-agent"
  postinst configure
  [ -L "$ROOT/usr/local/bin/lsh-agent" ]
  [ "$(readlink "$ROOT/usr/local/bin/lsh-agent")" = /usr/bin/lsh-agent ]
}

@test "configure restarts the agent onto the new binary (not just start)" {
  [ -d /run/systemd/system ] || skip "no systemd on this runner"
  postinst configure
  called "deb-systemd-invoke restart lsh-agent.service"
}
