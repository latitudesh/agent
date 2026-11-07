#!/usr/bin/env bats
# install.sh: UEKR8 netfilter modules (ensure_uek_netfilter_modules).
#
# Guards the Oracle Linux 10 failure found on bare metal: UEKR8 ships
# xt_limit/xt_LOG/xt_multiport/... in kernel-uek-modules-extra-netfilter, which
# nothing pulls in, so 'ufw enable' failed and the installer aborted.

load lib/agent-test

setup() {
  setup_scratch
  FUNCS="$(retry_policy)"$'\n'"$(extract_fn "$INSTALL_SH" ensure_uek_netfilter_modules)"
  DNF="dnf --setopt=retries=10 --setopt=timeout=30 --setopt=minrate=1000"
}

# run_uek <ID> <uname -r> <modinfo-rc> <dnf-rc> [family]
run_uek() {
  local id="$1" kver="$2" modinfo_rc="$3" dnf_rc="$4" family="${5:-rhel}"
  printf 'NAME="clobber"\nID=%s\nVERSION_ID="10.1"\n' "$id" > "$ROOT/os-release"
  stub uname 0 "$kver"
  stub modinfo "$modinfo_rc"
  stub dnf "$dnf_rc"
  run bash -c '
    PATH="$1"; OS_FAMILY="$4" RPM_PM=dnf OS_RELEASE_FILE="$2" NAME=keep
    eval "$3"
    ensure_uek_netfilter_modules > /dev/null || exit $?
    [ "$NAME" = keep ] || { echo "os-release leaked NAME=$NAME" >&2; exit 99; }
  ' _ "$(stub_path)" "$ROOT/os-release" "$FUNCS" "$family"
}

@test "OL10 on UEK without xt_limit installs the extra-netfilter package for the running kernel" {
  run_uek ol 6.12.0-208.112.5.el10uek.x86_64 1 0
  [ "$status" -eq 0 ]
  called "modinfo -k 6.12.0-208.112.5.el10uek.x86_64 xt_limit"
  called "$DNF install -y kernel-uek-modules-extra-netfilter-6.12.0-208.112.5.el10uek.x86_64"
}

@test "OL on UEK with the modules present (OL9/UEK7, fixed OL10 images): nothing installed" {
  run_uek ol 5.15.0-326.el9uek.x86_64 0 0
  [ "$status" -eq 0 ]
  not_called_with "dnf"
}

@test "OL on the Red Hat compatible kernel: skipped" {
  run_uek ol 6.12.0-55.el10.x86_64 1 0
  [ "$status" -eq 0 ]
  not_called_with "modinfo"
  not_called_with "dnf"
}

@test "other EL distros are skipped" {
  run_uek rocky 6.12.0-211.el10.x86_64 1 0
  [ "$status" -eq 0 ]
  not_called_with "dnf"
  run_uek almalinux 6.12.0-211.el10.x86_64 1 0
  [ "$status" -eq 0 ]
  not_called_with "dnf"
}

@test "a failing install fails the installer before UFW is touched" {
  run_uek ol 6.12.0-208.112.5.el10uek.x86_64 1 1
  [ "$status" -eq 1 ]
  called "$DNF install -y kernel-uek-modules-extra-netfilter-6.12.0-208.112.5.el10uek.x86_64"
}

@test "Debian family never touches it" {
  run_uek ol 6.12.0-208.112.5.el10uek.x86_64 1 0 debian
  [ "$status" -eq 0 ]
  [ ! -s "$CALLS" ]
}

@test "it runs before UFW is enabled" {
  call="$(grep -n '^ensure_uek_netfilter_modules$' "$INSTALL_SH" | cut -d: -f1)"
  enable="$(grep -n 'ufw --force enable' "$INSTALL_SH" | head -1 | cut -d: -f1)"
  [ -n "$call" ] && [ -n "$enable" ] && [ "$call" -lt "$enable" ]
}
