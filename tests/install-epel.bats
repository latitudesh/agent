#!/usr/bin/env bats
# install.sh: RHEL-family EPEL step (epel_package + enable_epel_if_needed).
#
# Guards the Oracle Linux 10 regression: OL has no "epel-release" package (its
# EPEL comes from oracle-epel-release-el<major>), so a hard-coded
# `dnf install epel-release` made the installer exit on every OL10 host.

load lib/agent-test

setup() {
  setup_scratch
  FUNCS="$(extract_fn "$INSTALL_SH" epel_package enable_epel_if_needed)"
}

# run_epel <ID> <VERSION_ID> <ufw:yes|no> <epel-rpm-installed:yes|no> <dnf-rc>
run_epel() {
  local id="$1" ver="$2" ufw="$3" installed="$4" dnf_rc="$5"
  printf 'NAME="clobber"\nID=%s\nVERSION_ID="%s"\n' "$id" "$ver" > "$ROOT/os-release"
  stub rpm "$([ "$installed" = yes ] && echo 0 || echo 1)"
  stub dnf "$dnf_rc"
  [ "$ufw" = yes ] && stub ufw
  run bash -c '
    PATH="$1"; OS_FAMILY=rhel RPM_PM=dnf OS_RELEASE_FILE="$2" NAME=keep
    eval "$3"
    enable_epel_if_needed > /dev/null || exit $?
    # os-release is read in a subshell: it must not leak into the installer.
    [ "$NAME" = keep ] || { echo "os-release leaked NAME=$NAME" >&2; exit 99; }
  ' _ "$(stub_path)" "$ROOT/os-release" "$FUNCS"
}

@test "Oracle Linux 10 without ufw installs oracle-epel-release-el10" {
  run_epel ol 10.0 no no 0
  [ "$status" -eq 0 ]
  called "dnf install -y oracle-epel-release-el10"
}

@test "Oracle Linux 9 without ufw installs oracle-epel-release-el9" {
  run_epel ol 9.6 no no 0
  [ "$status" -eq 0 ]
  called "dnf install -y oracle-epel-release-el9"
}

@test "AlmaLinux 9 without ufw installs epel-release" {
  run_epel almalinux 9.6 no no 0
  [ "$status" -eq 0 ]
  called "dnf install -y epel-release"
}

@test "Rocky 10 without ufw installs epel-release" {
  run_epel rocky 10.0 no no 0
  [ "$status" -eq 0 ]
  called "dnf install -y epel-release"
}

@test "ufw already present (every Latitude EL image): EPEL is skipped" {
  run_epel ol 10.0 yes no 0
  [ "$status" -eq 0 ]
  not_called_with "dnf"
  run_epel rocky 10.0 yes no 0
  [ "$status" -eq 0 ]
  not_called_with "dnf"
}

@test "EPEL release package already installed: nothing to install" {
  run_epel ol 10.0 no yes 0
  [ "$status" -eq 0 ]
  not_called_with "dnf"
}

@test "a failing EPEL install fails the installer" {
  run_epel ol 10.0 no no 1
  [ "$status" -eq 1 ]
  called "dnf install -y oracle-epel-release-el10"
}

@test "Debian family never touches EPEL" {
  run bash -c 'PATH="$1"; OS_FAMILY=debian RPM_PM=dnf OS_RELEASE_FILE=/nonexistent; eval "$2"; enable_epel_if_needed' \
    _ "$(stub_path)" "$FUNCS"
  [ "$status" -eq 0 ]
  [ ! -s "$CALLS" ]
}
