#!/usr/bin/env bash
# End-to-end test of install.sh and uninstall.sh on Debian/Ubuntu and the RHEL
# family, run the way the dashboard runs them: boots a systemd container
# (privileged, so UFW can drive netfilter), serves the packages under test from
# a local signed apt or dnf repository, and runs the installer over a host still
# carrying the source-built agent of the previous install.sh.
#
#   scripts/test-install-sh.sh <packages-dir> [image]    # image defaults to ubuntu:24.04
#
# <packages-dir> holds the .deb and/or .rpm files under test; the image picks
# which are used. If OTHER_RELEASE_DIR holds the packages of another release
# (CI passes the latest published one), they are added to the repository
# afterwards and install.sh is re-run the way LKS runs it: -version v<that
# release>, no -public_ip, then back to the version under test.
#
# Needs Docker. api.latitude.sh resolves to 127.0.0.1 inside the container, so
# the agent never reaches the production API.
#
# The single-quoted scripts passed to run() expand inside the container, not here.
# shellcheck disable=SC2016
set -euo pipefail

if [ $# -lt 1 ]; then
    echo "Usage: $0 <packages-dir> [image]" >&2
    exit 1
fi
pkgs_dir=$(realpath "$1")
image=${2:-ubuntu:24.04}
root=$(dirname "$(dirname "$(realpath "$0")")")

case "$image" in
    *almalinux* | *rockylinux* | *oraclelinux*) family=rhel ;;
    *) family=debian ;;
esac

# EPEL's ufw drives iptables-legacy, whose tables live in kernel modules. A real
# server loads them on demand; a container cannot, so the host must have them.
if [ "$family" = rhel ]; then
    for module in iptable_filter ip6table_filter; do
        if [ ! -e "/sys/module/$module" ]; then
            echo "Kernel module $module is not loaded; run 'sudo modprobe $module' on this host first." >&2
            exit 1
        fi
    done
fi

# The version of this host's package in a directory (empty when there is none).
case "$(uname -m)" in
    x86_64) deb_arch=amd64 rpm_arch=x86_64 ;;
    aarch64) deb_arch=arm64 rpm_arch=aarch64 ;;
    *) echo "Unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
package_version() {
    if [ "$family" = debian ]; then
        find "$1" -maxdepth 1 -name "lsh-agent_*_${deb_arch}.deb" -printf '%f\n' |
            sed -e 's/^lsh-agent_//' -e "s/_${deb_arch}\.deb\$//" | head -n 1
    else
        find "$1" -maxdepth 1 -name "lsh-agent-*.${rpm_arch}.rpm" -printf '%f\n' |
            sed -e 's/^lsh-agent-//' -e "s/-[^-]*\.${rpm_arch}\.rpm\$//" | head -n 1
    fi
}
version=$(package_version "$pkgs_dir")
if [ -z "$version" ]; then
    echo "No $family package for this architecture in $pkgs_dir" >&2
    exit 1
fi
other_dir=${OTHER_RELEASE_DIR:+$(realpath "$OTHER_RELEASE_DIR")}
other=${other_dir:+$(package_version "$other_dir")}
if [ "$other" = "$version" ]; then
    other=
fi

# Turn the stock image into something that boots like a server before handing
# PID 1 to systemd: procps (sysctl, which ufw calls) is on every real server
# but not in the container images, and the Debian ones also ship a policy-rc.d
# that blocks service starts from maintainer scripts. ufw comes preinstalled but
# inactive, as on current Latitude images (from EPEL on the RHEL family), which
# also keeps install.sh from downloading anything off the distribution archive
# during the test. Installing at start-up rather than in a docker build leaves
# no image or build cache behind.
#
# Mirrors behind one hostname can be out of sync (an index listing files one
# server does not have yet, 404). Retry with a fresh index and DNS lookup rather
# than fail the job on the archive, using the install.sh retry policy on every
# call. Give up loudly: carrying on would boot a container without systemd.
if [ "$family" = debian ]; then
    boot='
        net="-o Acquire::Retries=5 -o Acquire::Retries::Delay=true -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30"
        for attempt in 1 2 3; do
            apt-get $net update -qq &&
                DEBIAN_FRONTEND=noninteractive apt-get $net install -y -qq --no-install-recommends \
                    systemd systemd-sysv dbus procps ufw ca-certificates curl gnupg apt-utils iproute2 > /dev/null &&
                break
            echo "Installing systemd failed (attempt $attempt of 3)" >&2
            [ "$attempt" = 3 ] && exit 1
            sleep 60
        done
        rm -f /usr/sbin/policy-rc.d
        exec /sbin/init
    '
else
    boot='
        net="--setopt=retries=10 --setopt=timeout=30 --setopt=minrate=1000"
        . /etc/os-release
        epel=epel-release
        [ "$ID" = ol ] && epel=oracle-epel-release-el${VERSION_ID%%.*}
        for attempt in 1 2 3; do
            dnf $net install -y -q "$epel" > /dev/null &&
                dnf $net install -y -q systemd procps-ng ufw ca-certificates gnupg2 createrepo_c \
                    iproute hostname findutils > /dev/null &&
                break
            echo "Installing systemd failed (attempt $attempt of 3)" >&2
            [ "$attempt" = 3 ] && exit 1
            sleep 60
        done
        exec /usr/sbin/init
    '
fi
cid=$(docker run --detach --privileged --cgroupns=host \
    --volume /sys/fs/cgroup:/sys/fs/cgroup:rw \
    --volume "$root:/src:ro" --volume "$pkgs_dir:/pkgs:ro" --volume "${other_dir:-$pkgs_dir}:/other:ro" \
    --add-host api.latitude.sh:127.0.0.1 \
    "$image" bash -c "$boot")
trap 'docker rm --force "$cid" > /dev/null' EXIT

run() { docker exec "$cid" bash -euo pipefail -c "$1"; }
install_sh() {
    docker exec --env LSH_AGENT_APT_REPO=file:///srv/apt --env LSH_AGENT_RPM_REPO=file:///srv/rpm \
        "$cid" bash /src/install.sh "$@"
}
if [ "$family" = debian ]; then
    installed_version() { run 'dpkg-query -W -f="\${Version}" lsh-agent'; }
else
    installed_version() { run 'rpm -q --qf "%{VERSION}" lsh-agent'; }
fi

# Wait for boot to finish before touching the system: until then there is no
# bus to talk to, and systemd may still mount a fresh /tmp over our files.
# Degraded is fine: some units never start in a container.
for _ in $(seq 300); do
    if [ "$(docker inspect --format '{{.State.Running}}' "$cid")" != true ]; then
        docker logs "$cid" >&2
        echo "The container exited before systemd booted" >&2
        exit 1
    fi
    state=$(docker exec "$cid" systemctl is-system-running 2> /dev/null || true)
    case "$state" in running | degraded) break ;; esac
    sleep 1
done
case "$state" in
    running | degraded) ;;
    *) echo "systemd did not finish booting (state: ${state:-unknown})" >&2; exit 1 ;;
esac

# (Re)builds the local repository from the given directories, signed with
# one key generated on first use.
build_repo() {
    docker exec "$cid" bash -euo pipefail -c '
        export GNUPGHOME=/srv/gnupg
        if [ ! -d "$GNUPGHOME" ]; then
            mkdir -p -m 0700 "$GNUPGHOME"
            gpg --batch --passphrase "" --quick-gen-key "lsh-agent CI <ci@example.invalid>" rsa3072 sign never 2> /dev/null
        fi
        rm -rf /srv/in && mkdir -p /srv/in
        for d in "$@"; do cp "$d"/lsh-agent* /srv/in/; done
        if command -v dpkg-query > /dev/null; then
            REPO_URL=file:/srv/apt bash /src/scripts/build-apt-repo.sh /srv/in /srv/apt
        else
            REPO_URL=file:///srv/rpm bash /src/scripts/build-rpm-repo.sh /srv/in /srv/rpm
            dnf clean all -q 2> /dev/null || true
        fi
        chmod -R a+rX /srv/apt /srv/rpm 2> /dev/null || true
    ' build-repo "$@"
}

echo "== local $family repository with the package under test ($version)"
build_repo /pkgs

echo "== host set up by the previous install.sh (agent built from source, running)"
run '
    mkdir -p /etc/lsh-agent
    cp /src/configs/agent.yaml /etc/lsh-agent/config.yaml
    printf "FIREWALL_ID=fw_old\nPROJECT_ID=proj_old\n" > /etc/lsh-agent/env
    printf "#!/bin/sh\nexec sleep infinity\n" > /usr/local/bin/lsh-agent
    chmod +x /usr/local/bin/lsh-agent
    printf "%s\n" "[Unit]" "Description=Latitude.sh Agent" "After=network.target" "Wants=network.target" "" \
        "[Service]" "Type=simple" "ExecStart=/usr/local/bin/lsh-agent -config /etc/lsh-agent/config.yaml" \
        "Restart=always" "RestartSec=10" "User=root" "" "[Install]" "WantedBy=multi-user.target" \
        > /etc/systemd/system/lsh-agent.service
    systemctl daemon-reload
    systemctl enable --now lsh-agent.service
    systemctl is-active --quiet lsh-agent.service
'

echo "== install.sh"
install_sh -firewall fw_test -project proj_test -public_ip 192.0.2.10

echo "== agent runs the packaged binary; host migrated"
run '
    systemctl is-active --quiet lsh-agent.service
    systemctl is-enabled --quiet lsh-agent.service
    pid=$(systemctl show --property MainPID --value lsh-agent.service)
    test "$(readlink "/proc/$pid/exe")" = /usr/bin/lsh-agent
    test ! -e /etc/systemd/system/lsh-agent.service
    test "$(readlink /usr/local/bin/lsh-agent)" = /usr/bin/lsh-agent
    grep -qx FIREWALL_ID=fw_test /etc/lsh-agent/env
    grep -qx PUBLIC_IP=192.0.2.10 /etc/lsh-agent/env
    lsh-agent -version
    # Config loaded and the sync loop started (the API itself is unreachable here).
    journalctl -u lsh-agent.service --no-pager | grep -q "Starting agent with 30s interval"
'

echo "== UFW active with the installer defaults"
run '
    ufw status verbose | grep -q "Status: active"
    ufw status verbose | grep -q "Default: deny (incoming), allow (outgoing)"
    ufw status | grep -Eq "^(22/tcp|22|OpenSSH) +ALLOW"
'

test "$(installed_version)" = "$version"

echo "== install.sh again, pinned to the packaged version, over the running agent"
install_sh -firewall fw_test -project proj_test -public_ip 192.0.2.10 -version "$version"
run 'systemctl is-active --quiet lsh-agent.service'

# The call LKS makes on every node (lks-platform bootstrap-deps.sh): the git tag
# with its v, no -public_ip, and the node pool may pin a release other than the
# newest. It must install exactly that version, run it, and move back.
if [ -n "$other" ]; then
    echo "== install.sh as LKS calls it: -version v$other, then v$version"
    build_repo /pkgs /other
    install_sh -firewall fw_lks -project proj_lks -version "v$other"
    test "$(installed_version)" = "$other"
    run '
        systemctl is-active --quiet lsh-agent.service
        pid=$(systemctl show --property MainPID --value lsh-agent.service)
        test "$(readlink "/proc/$pid/exe")" = /usr/bin/lsh-agent
        grep -qx FIREWALL_ID=fw_lks /etc/lsh-agent/env
    '
    install_sh -firewall fw_lks -project proj_lks -version "v$version"
    test "$(installed_version)" = "$version"
    run 'systemctl is-active --quiet lsh-agent.service'
else
    echo "== no other $family release given (OTHER_RELEASE_DIR): skipping the LKS-style pinned install"
fi

echo "== uninstall.sh"
run 'bash /src/uninstall.sh'
# "! cmd" would not trip errexit, so negative checks fail explicitly.
run '
    if command -v dpkg-query > /dev/null; then
        if dpkg-query -W -f="\${Status}" lsh-agent 2> /dev/null | grep -q "ok installed"; then
            echo "lsh-agent is still installed" >&2; exit 1
        fi
        test ! -e /etc/apt/sources.list.d/lsh-agent.sources
    else
        if rpm -q lsh-agent > /dev/null; then
            echo "lsh-agent is still installed" >&2; exit 1
        fi
        test ! -e /etc/yum.repos.d/lsh-agent.repo
        rpm -q ufw > /dev/null # the firewall package stays
    fi
    test ! -e /usr/local/bin/lsh-agent
    test ! -e /etc/lsh-agent
    if systemctl is-active --quiet lsh-agent.service; then
        echo "lsh-agent.service is still active" >&2; exit 1
    fi
'

echo "OK"
