#!/usr/bin/env bash
# Smoke-tests the .rpm the way test-apt-install.sh tests the .deb: installs
# lsh-agent over what install.sh used to leave behind (a source build), checks
# the migration, keeps a changed config across a reinstall, then removes it.
#
# Run as root in a disposable RHEL-family container (it rewrites system files),
# e.g.:
#
#   docker run --rm -v "$PWD:/src" -w /src almalinux:10 \
#       bash scripts/test-rpm-install.sh dist
set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 <rpms-dir>" >&2
    exit 1
fi
rpms_dir=$(realpath "$1")

# install.sh's dnf policy, with an outer retry for mirror syncs.
dnf_net=(--setopt=retries=10 --setopt=timeout=30 --setopt=minrate=1000)
dnf_install() {
    local attempt
    for attempt in 1 2 3; do
        dnf "${dnf_net[@]}" install -y "$@" && return 0
        echo "dnf install failed (attempt $attempt of 3)" >&2
        [ "$attempt" = 3 ] || sleep 60
    done
    return 1
}

# ufw comes from EPEL. Oracle Linux names its EPEL package differently, as
# install.sh's epel_package() does.
. /etc/os-release
if [ "$ID" = ol ]; then
    dnf_install "oracle-epel-release-el${VERSION_ID%%.*}"
else
    dnf_install epel-release
fi

shopt -s nullglob
rpms=("$rpms_dir"/lsh-agent-*."$(uname -m)".rpm)
if [ ${#rpms[@]} -ne 1 ]; then
    echo "Expected one $(uname -m) .rpm in $rpms_dir, found ${#rpms[@]}" >&2
    exit 1
fi
rpm_file=${rpms[0]}

# What install.sh used to set up on the host.
install -d /etc/systemd/system/multi-user.target.wants /etc/lsh-agent /usr/local/bin
cat > /etc/systemd/system/lsh-agent.service << 'UNIT'
[Service]
ExecStart=/usr/local/bin/lsh-agent -config /etc/lsh-agent/config.yaml
UNIT
ln -s /etc/systemd/system/lsh-agent.service /etc/systemd/system/multi-user.target.wants/lsh-agent.service
printf '#!/bin/sh\n' > /usr/local/bin/lsh-agent
chmod +x /usr/local/bin/lsh-agent
printf 'FIREWALL_ID=fw_test\nPROJECT_ID=proj_test\n' > /etc/lsh-agent/env

# ufw installed on its own first, as install.sh does: dnf removes packages
# that came in only as a dependency when the package that pulled them is
# removed, and removing the agent must never take the host firewall with it.
dnf_install ufw
dnf_install "$rpm_file"

echo "== installed"
lsh-agent -version
lsh-agent -check-config # needs the env file above and ufw, pulled in as a dependency
test -f /usr/lib/systemd/system/lsh-agent.service
test "$(stat -c %a /usr/lib/systemd/system/lsh-agent.service)" = 644
test "$(stat -c %a /etc/lsh-agent/config.yaml)" = 644

echo "== migrated from install.sh"
test ! -e /etc/systemd/system/lsh-agent.service
test "$(readlink /usr/local/bin/lsh-agent)" = /usr/bin/lsh-agent
# Enabled on first install, against the packaged unit.
test "$(readlink -f /etc/systemd/system/multi-user.target.wants/lsh-agent.service)" = /usr/lib/systemd/system/lsh-agent.service

echo "== a changed config survives a reinstall"
printf '\n# local change\n' >> /etc/lsh-agent/config.yaml
dnf "${dnf_net[@]}" reinstall -y "$rpm_file"
grep -q '^# local change$' /etc/lsh-agent/config.yaml

echo "== removed"
dnf remove -y lsh-agent
test ! -e /usr/bin/lsh-agent
test ! -L /usr/local/bin/lsh-agent
test ! -e /etc/systemd/system/multi-user.target.wants/lsh-agent.service
# RPM has no purge: the env file install.sh wrote stays (uninstall.sh removes it).
test -f /etc/lsh-agent/env
# Removing the agent keeps the host firewall package (installed on its own).
rpm -q ufw

echo "OK"
