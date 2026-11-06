#!/usr/bin/env bash
# Builds the signed dnf repository served at https://packages.lsh.io/rpm, the
# RPM counterpart of build-apt-repo.sh and signed with the same key:
#
#   <out>/packages/lsh-agent-<version>-1.<arch>.rpm   every release, both arches
#   <out>/repodata/                                   createrepo_c metadata
#   <out>/repodata/repomd.xml.asc                     detached signature
#   <out>/lsh-agent.asc                               public key
#   <out>/lsh-agent.repo                              the one file clients add
#
# Like the apt repository, the packages themselves stay as GoReleaser built
# them (so their digests match the release assets, SBOMs and provenance
# attestations) and the signed metadata carries their checksums:
# repo_gpgcheck=1 verifies repomd.xml, which pins primary.xml, which pins the
# SHA-256 of every package dnf downloads.
#
#   scripts/build-rpm-repo.sh <rpms-dir> <output-dir>
#
# Signs with SIGNING_KEY (a key ID or fingerprint), or the first secret key in
# the GnuPG keyring. REPO_URL overrides the URL written into lsh-agent.repo.
set -euo pipefail

REPO_URL=${REPO_URL:-https://packages.lsh.io/rpm}

if [ $# -ne 2 ]; then
    echo "Usage: $0 <rpms-dir> <output-dir>" >&2
    exit 1
fi
rpms_dir=$1
out=$2

shopt -s nullglob
rpms=("$rpms_dir"/*.rpm)
if [ ${#rpms[@]} -eq 0 ]; then
    echo "No .rpm files in $rpms_dir" >&2
    exit 1
fi

key=${SIGNING_KEY:-$(gpg --batch --list-secret-keys --with-colons | awk -F: '$1 == "fpr" { print $10; exit }')}
if [ -z "$key" ]; then
    echo "No secret key in the GnuPG keyring to sign the repository with" >&2
    exit 1
fi

rm -rf "$out"
mkdir -p "$out/packages"
cp "${rpms[@]}" "$out/packages/"
cd "$out"

# One repository for both architectures: dnf only considers the packages that
# match the host. sha256 checksums; no timestamps in the metadata file names
# would break nothing, but unique names keep CDN caches honest.
createrepo_c --quiet --checksum sha256 --unique-md-filenames .

gpg --batch --yes --local-user "$key" --digest-algo SHA512 \
    --armor --detach-sign --output repodata/repomd.xml.asc repodata/repomd.xml
gpg --batch --armor --export "$key" > lsh-agent.asc

# gpgcheck=0 is deliberate and matches the apt repository: the packages are not
# signed one by one (see the header), the repository metadata is.
cat > lsh-agent.repo << REPO
[lsh-agent]
name=Latitude.sh Agent
baseurl=$REPO_URL
enabled=1
repo_gpgcheck=1
gpgcheck=0
gpgkey=$REPO_URL/lsh-agent.asc
REPO

echo "Built the dnf repository with ${#rpms[@]} package(s), signed by $key"
