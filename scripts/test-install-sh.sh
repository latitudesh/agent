#!/usr/bin/env bash
# End-to-end test of install.sh and uninstall.sh on Debian/Ubuntu, run the way
# the dashboard runs them: boots a systemd container (privileged, so UFW can
# drive netfilter), serves the given .debs from a local signed apt repository,
# and runs the installer over a host still carrying the source-built agent of
# the previous install.sh.
#
#   scripts/test-install-sh.sh <debs-dir> [image]    # image defaults to ubuntu:24.04
#
# Needs Docker. api.latitude.sh resolves to 127.0.0.1 inside the container, so
# the agent never reaches the production API.
#
# The single-quoted scripts passed to run() expand inside the container, not here.
# shellcheck disable=SC2016
set -euo pipefail

if [ $# -lt 1 ]; then
    echo "Usage: $0 <debs-dir> [image]" >&2
    exit 1
fi
debs_dir=$(realpath "$1")
image=${2:-ubuntu:24.04}
root=$(dirname "$(dirname "$(realpath "$0")")")

# Turn the stock image into something that boots like a server before handing
# PID 1 to systemd: procps (sysctl, which ufw calls) is on every real server
# but not in the Debian images, and those images also ship a policy-rc.d that
# blocks service starts from maintainer scripts. Installing at start-up rather
# than in a docker build leaves no image or build cache behind.
cid=$(docker run --detach --privileged --cgroupns=host \
    --volume /sys/fs/cgroup:/sys/fs/cgroup:rw \
    --volume "$root:/src:ro" --volume "$debs_dir:/debs:ro" \
    --add-host api.latitude.sh:127.0.0.1 \
    "$image" bash -c '
        apt-get update -qq
        DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
            systemd systemd-sysv dbus procps ca-certificates curl gnupg apt-utils iproute2 > /dev/null
        rm -f /usr/sbin/policy-rc.d
        exec /sbin/init
    ')
trap 'docker rm --force "$cid" > /dev/null' EXIT

run() { docker exec "$cid" bash -euo pipefail -c "$1"; }
install_sh() { docker exec --env LSH_AGENT_APT_REPO=file:///srv/apt "$cid" bash /src/install.sh "$@"; }

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

echo "== local apt repository"
run '
    export GNUPGHOME=$(mktemp -d)
    gpg --batch --passphrase "" --quick-gen-key "lsh-agent CI <ci@example.invalid>" rsa3072 sign never 2> /dev/null
    REPO_URL=file:/srv/apt bash /src/scripts/build-apt-repo.sh /debs /srv/apt
    chmod -R a+rX /srv/apt
'

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

echo "== install.sh again, pinned to the packaged version, over the running agent"
version=$(run 'dpkg-query -W -f="\${Version}" lsh-agent')
install_sh -firewall fw_test -project proj_test -public_ip 192.0.2.10 -version "$version"
run 'systemctl is-active --quiet lsh-agent.service'

echo "== uninstall.sh"
run 'bash /src/uninstall.sh'
run '
    ! dpkg-query -W -f="\${Status}" lsh-agent 2> /dev/null | grep -q "ok installed"
    test ! -e /etc/apt/sources.list.d/lsh-agent.sources
    test ! -e /usr/local/bin/lsh-agent
    test ! -e /etc/lsh-agent
    ! systemctl is-active --quiet lsh-agent.service
'

echo "OK"
