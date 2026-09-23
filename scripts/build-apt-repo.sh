#!/usr/bin/env bash
# Builds a signed apt repository from a directory of .deb files.
#
#   scripts/build-apt-repo.sh <debs-dir> <output-dir>
#
# Output layout (published at https://packages.lsh.io/apt):
#
#   lsh-agent.asc                                  public signing key
#   pool/main/l/lsh-agent/*.deb
#   dists/stable/{Release,Release.gpg,InRelease}
#   dists/stable/main/binary-<arch>/Packages{,.gz}
#
# Signs with the secret key in the current GnuPG keyring: SIGNING_KEY
# (fingerprint) if set, otherwise the first secret key found.
# Requires apt-ftparchive (apt-utils), dpkg-deb and gpg.
set -euo pipefail

SUITE=stable
COMPONENT=main
POOL=pool/$COMPONENT/l/lsh-agent

if [ $# -ne 2 ]; then
    echo "Usage: $0 <debs-dir> <output-dir>" >&2
    exit 1
fi
debs_dir=$1
out=$2

shopt -s nullglob
debs=("$debs_dir"/*.deb)
if [ ${#debs[@]} -eq 0 ]; then
    echo "No .deb files in $debs_dir" >&2
    exit 1
fi

key=${SIGNING_KEY:-$(gpg --batch --list-secret-keys --with-colons | awk -F: '$1 == "fpr" { print $10; exit }')}
if [ -z "$key" ]; then
    echo "No secret key in the GnuPG keyring to sign the repository with" >&2
    exit 1
fi

rm -rf "$out"
mkdir -p "$out/$POOL"
cp "${debs[@]}" "$out/$POOL/"
cd "$out"

# apt-ftparchive writes Filename: paths relative to where it runs, which must be
# the repository root (the URL in sources.list).
archs=$(for deb in "$POOL"/*.deb; do dpkg-deb --field "$deb" Architecture; done | sort -u | xargs)
for arch in $archs; do
    dir=dists/$SUITE/$COMPONENT/binary-$arch
    mkdir -p "$dir"
    apt-ftparchive --arch "$arch" packages "$POOL" > "$dir/Packages"
    gzip -9 --no-name --keep "$dir/Packages"
done

# Generate Release outside dists/: apt-ftparchive would otherwise hash the
# half-written file into itself.
release=$(mktemp)
apt-ftparchive \
    -o APT::FTPArchive::Release::Origin="Latitude.sh" \
    -o APT::FTPArchive::Release::Label="Latitude.sh" \
    -o APT::FTPArchive::Release::Suite="$SUITE" \
    -o APT::FTPArchive::Release::Codename="$SUITE" \
    -o APT::FTPArchive::Release::Components="$COMPONENT" \
    -o APT::FTPArchive::Release::Architectures="$archs" \
    -o APT::FTPArchive::Release::Description="Latitude.sh packages" \
    release "dists/$SUITE" > "$release"
mv "$release" "dists/$SUITE/Release"
chmod 0644 "dists/$SUITE/Release"

gpg_sign=(gpg --batch --yes --local-user "$key" --digest-algo SHA512)
"${gpg_sign[@]}" --armor --detach-sign --output "dists/$SUITE/Release.gpg" "dists/$SUITE/Release"
"${gpg_sign[@]}" --clearsign --output "dists/$SUITE/InRelease" "dists/$SUITE/Release"
gpg --batch --armor --export "$key" > lsh-agent.asc

echo "Built $SUITE ($archs) with ${#debs[@]} package(s), signed by $key"
