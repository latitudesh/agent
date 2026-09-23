#!/usr/bin/env bash
# Unit test for install.sh's RHEL-family EPEL step (epel_package +
# enable_epel_if_needed). No Docker, no root: the two functions are extracted
# from install.sh and run against a fake os-release and stub rpm/dnf/ufw on a
# PATH that holds nothing else, so the runner's own ufw is never seen.
#
#   scripts/test-install-epel.sh
#
# Guards the Oracle Linux 10 regression: OL has no "epel-release" package (its
# EPEL comes from oracle-epel-release-el<major>), so a hard-coded
# `dnf install epel-release` made the installer exit on every OL10 host.
set -euo pipefail

root=$(dirname "$(dirname "$(realpath "$0")")")
funcs=$(sed -n '/^epel_package() {$/,/^}$/p; /^enable_epel_if_needed() {$/,/^}$/p' "$root/install.sh")
[ -n "$funcs" ] || { echo "FAIL: could not extract the EPEL functions from install.sh" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

# case <name> <ID> <VERSION_ID> <ufw:yes|no> <rpm-installed:yes|no> <dnf-rc> <want-rc> <want-dnf-args|->
case_() {
    local name=$1 id=$2 ver=$3 ufw=$4 installed=$5 dnf_rc=$6 want_rc=$7 want_dnf=$8
    local d="$work/$name" rc=0 got
    mkdir -p "$d/bin"
    printf 'NAME="clobber"\nID=%s\nVERSION_ID="%s"\n' "$id" "$ver" > "$d/os-release"
    printf '#!/bin/sh\nexit %s\n' "$([ "$installed" = yes ] && echo 0 || echo 1)" > "$d/bin/rpm"
    printf '#!/bin/sh\necho "$*" >> "%s/dnf.log"\nexit %s\n' "$d" "$dnf_rc" > "$d/bin/dnf"
    [ "$ufw" = yes ] && printf '#!/bin/sh\n' > "$d/bin/ufw"
    chmod +x "$d/bin/"*

    (
        PATH="$d/bin:/usr/bin:/bin"
        # shellcheck disable=SC2034  # read by the extracted functions
        OS_FAMILY=rhel RPM_PM=dnf OS_RELEASE_FILE="$d/os-release" NAME=keep
        eval "$funcs"
        enable_epel_if_needed > /dev/null
        [ "$NAME" = keep ] || { echo "os-release leaked NAME=$NAME" >&2; exit 99; }
    ) || rc=$?

    got=$(cat "$d/dnf.log" 2> /dev/null || echo -)
    if [ "$rc" = "$want_rc" ] && [ "$got" = "$want_dnf" ]; then
        echo "ok   $name"
    else
        echo "FAIL $name: rc=$rc (want $want_rc), dnf='$got' (want '$want_dnf')"
        fail=1
    fi
}

#     name                      ID         VERSION_ID  ufw  rpm-q  dnf-rc  rc  dnf args
case_ ol10-no-ufw               ol         10.0        no   no     0       0   "install -y oracle-epel-release-el10"
case_ ol9-no-ufw                ol         9.6         no   no     0       0   "install -y oracle-epel-release-el9"
case_ alma9-no-ufw              almalinux  9.6         no   no     0       0   "install -y epel-release"
case_ rocky10-no-ufw            rocky      10.0        no   no     0       0   "install -y epel-release"
case_ ol10-ufw-present          ol         10.0        yes  no     0       0   -
case_ rocky10-ufw-present       rocky      10.0        yes  no     0       0   -
case_ ol10-epel-already         ol         10.0        no   yes    0       0   -
case_ epel-install-fails        ol         10.0        no   no     1       1   "install -y oracle-epel-release-el10"

# Debian family never touches EPEL.
(
    PATH="$work/none:/usr/bin:/bin"
    # shellcheck disable=SC2034
    OS_FAMILY=debian RPM_PM=dnf OS_RELEASE_FILE=/nonexistent
    eval "$funcs"
    enable_epel_if_needed
) && echo "ok   debian-family-skips" || { echo "FAIL debian-family-skips"; fail=1; }

exit "$fail"
