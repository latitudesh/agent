#!/usr/bin/env bash
# Builds a signed apt repository from a directory of .deb files.
#
#   scripts/build-apt-repo.sh <debs-dir> <output-dir>
#
# Output layout (published at https://packages.lsh.io/apt):
#
#   lsh-agent.asc                                  public signing key
#   lsh-agent.sources                              apt source with the key inline
#   pool/main/l/lsh-agent/*.deb
#   dists/stable/{Release,Release.gpg,InRelease}
#   dists/stable/main/binary-<arch>/Packages{,.gz}
#
# Signs with the secret key in the current GnuPG keyring: SIGNING_KEY
# (fingerprint) if set, otherwise the first secret key found. REPO_URL is the
# URL written into lsh-agent.sources (override it to test a local repository).
# Requires apt-ftparchive (apt-utils), dpkg-deb and gpg.
set -euo pipefail

SUITE=stable
COMPONENT=main
POOL=pool/$COMPONENT/l/lsh-agent
REPO_URL=${REPO_URL:-https://packages.lsh.io/apt}

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

# Clients set the repository up with this one file: a deb822 source whose
# Signed-By carries the key inline (apt >= 2.3.10, i.e. Ubuntu 22.04 and
# Debian 12 onwards). Continuation lines are indented, blank ones become " .".
{
    printf 'Types: deb\nURIs: %s\nSuites: %s\nComponents: %s\nSigned-By:\n' "$REPO_URL" "$SUITE" "$COMPONENT"
    sed -e 's/^$/./' -e 's/^/ /' lsh-agent.asc
} > lsh-agent.sources

echo "Built $SUITE ($archs) with ${#debs[@]} package(s), signed by $key"
