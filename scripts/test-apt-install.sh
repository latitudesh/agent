#!/usr/bin/env bash
# Smoke-tests the .deb through a real apt repository: builds a signed repo from
# the given .debs with a throwaway key, installs lsh-agent from it over what
# install.sh used to leave behind, checks the migration, then purges.
#
# Run as root in a disposable Debian/Ubuntu container (it rewrites system
# files), e.g.:
#
#   docker run --rm --platform linux/amd64 -v "$PWD:/src" -w /src ubuntu:24.04 \
#       bash scripts/test-apt-install.sh dist
set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 <debs-dir>" >&2
    exit 1
fi
debs_dir=$(realpath "$1")
root=$(dirname "$(dirname "$(realpath "$0")")")

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends apt-utils gnupg ca-certificates > /dev/null

# The key is throwaway; the repository layout and signatures are the real thing.
GNUPGHOME=$(mktemp -d)
export GNUPGHOME
gpg --batch --passphrase '' --quick-gen-key 'lsh-agent CI <ci@example.invalid>' rsa3072 sign never
repo=$(mktemp -d)
REPO_URL="file:$repo/apt" bash "$root/scripts/build-apt-repo.sh" "$debs_dir" "$repo/apt"
chmod -R a+rX "$repo"

echo "== landing page lists the packages"
bash "$root/scripts/build-landing-page.sh" "$repo/apt" "$root/packaging/site/index.html" "$repo/index.html"
grep -q '<a href="/apt/pool/main/l/lsh-agent/lsh-agent_' "$repo/index.html"
if grep -q '<!-- PACKAGES -->' "$repo/index.html"; then
    echo "The package list placeholder was left in the page" >&2
    exit 1
fi

# Set the repository up the way users do: the one .sources file, key inline.
cp "$repo/apt/lsh-agent.sources" /etc/apt/sources.list.d/lsh-agent.sources

# What install.sh used to set up on the host.
install -d /etc/systemd/system/multi-user.target.wants /etc/lsh-agent /usr/local/bin
cat > /etc/systemd/system/lsh-agent.service << 'EOF'
[Service]
ExecStart=/usr/local/bin/lsh-agent -config /etc/lsh-agent/config.yaml
EOF
ln -s /etc/systemd/system/lsh-agent.service /etc/systemd/system/multi-user.target.wants/lsh-agent.service
printf '#!/bin/sh\n' > /usr/local/bin/lsh-agent
chmod +x /usr/local/bin/lsh-agent
cp "$root/configs/agent.yaml" /etc/lsh-agent/config.yaml
printf 'FIREWALL_ID=fw_test\nPROJECT_ID=proj_test\n' > /etc/lsh-agent/env

apt-get update -qq
apt-get install -y lsh-agent

echo "== installed from the repository"
lsh-agent -version
lsh-agent -check-config # needs the env file above and ufw, pulled in as a dependency
test -f /usr/lib/systemd/system/lsh-agent.service

echo "== migrated from install.sh"
test ! -e /etc/systemd/system/lsh-agent.service
test "$(readlink /usr/local/bin/lsh-agent)" = /usr/bin/lsh-agent
test -e /etc/systemd/system/multi-user.target.wants/lsh-agent.service # enabled, against the packaged unit

echo "== purged"
apt-get purge -y lsh-agent
test ! -e /usr/bin/lsh-agent
test ! -L /usr/local/bin/lsh-agent
test ! -e /etc/lsh-agent
test ! -L /etc/systemd/system/lsh-agent.service
test ! -L /etc/systemd/system/multi-user.target.wants/lsh-agent.service

echo "OK"
