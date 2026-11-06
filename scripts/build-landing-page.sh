#!/usr/bin/env bash
# Renders the packages.lsh.io landing page: the template with one table row per
# package, newest version first, in place of the <!-- PACKAGES --> line (apt)
# and the <!-- RPM_PACKAGES --> line (dnf). The apt rows come from the Packages
# indexes the repository serves, the dnf rows from the packages/ directory
# createrepo_c indexed, so the page lists exactly what apt and dnf can install.
#
#   scripts/build-landing-page.sh <apt-repo-dir> <template> <output> [<rpm-repo-dir>]
set -euo pipefail

if [ $# -ne 3 ] && [ $# -ne 4 ]; then
    echo "Usage: $0 <apt-repo-dir> <template> <output> [<rpm-repo-dir>]" >&2
    exit 1
fi
apt_dir=$1
template=$2
out=$3
rpm_dir=${4:-}

rows=$(mktemp)
rpm_rows=$(mktemp)
trap 'rm -f "$rows" "$rpm_rows"' EXIT

# Packages stanzas are blank-line separated "Field: value" lines (RS= reads one
# stanza per record). Portable awk only: the runner and Debian ship mawk.
cat "$apt_dir"/dists/stable/main/binary-*/Packages |
    awk 'BEGIN { RS = ""; FS = "\n" }
        {
            split("", f)
            for (i = 1; i <= NF; i++) {
                sep = index($i, ": ")
                if (sep > 1) f[substr($i, 1, sep - 1)] = substr($i, sep + 2)
            }
            printf "%s\t%s\t%s\t%.1f MB\t%s\n", f["Version"], f["Architecture"], f["Filename"], f["Size"] / 1048576, f["SHA256"]
        }' |
    sort -t "$(printf '\t')" -k1,1Vr -k2,2 |
    while IFS="$(printf '\t')" read -r version arch file size sha; do
        printf '      <tr><td>%s</td><td>%s</td><td><a href="/apt/%s">%s</a></td><td>%s</td><td><code title="%s">%s…</code></td></tr>\n' \
            "$version" "$arch" "$file" "${file##*/}" "$size" "$sha" "${sha:0:12}"
    done > "$rows"

if [ ! -s "$rows" ]; then
    echo "No packages found under $apt_dir" >&2
    exit 1
fi

# dnf rows: lsh-agent-<version>-<release>.<arch>.rpm, version and arch read
# from the file name GoReleaser gives every package.
if [ -n "$rpm_dir" ]; then
    for file in "$rpm_dir"/packages/*.rpm; do
        [ -e "$file" ] || continue
        name=${file##*/}
        base=${name%.rpm}
        arch=${base##*.}
        version=${base%.*}
        version=${version#lsh-agent-}
        version=${version%-*}
        size=$(stat -c %s "$file")
        sha=$(sha256sum "$file" | cut -d' ' -f1)
        printf '%s\t%s\t%s\t%s\t%s\n' "$version" "$arch" "$name" "$size" "$sha"
    done |
        sort -t "$(printf '\t')" -k1,1Vr -k2,2 |
        while IFS="$(printf '\t')" read -r version arch name size sha; do
            printf '      <tr><td>%s</td><td>%s</td><td><a href="/rpm/packages/%s">%s</a></td><td>%s MB</td><td><code title="%s">%s…</code></td></tr>\n' \
                "$version" "$arch" "$name" "$name" "$(awk -v s="$size" 'BEGIN { printf "%.1f", s / 1048576 }')" "$sha" "${sha:0:12}"
        done > "$rpm_rows"
    if [ ! -s "$rpm_rows" ]; then
        echo "No packages found under $rpm_dir/packages" >&2
        exit 1
    fi
fi

sed -e "/<!-- PACKAGES -->/{
r $rows
d
}" -e "/<!-- RPM_PACKAGES -->/{
r $rpm_rows
d
}" "$template" > "$out"
