#!/usr/bin/env bash
# Renders the packages.lsh.io landing page: the template with one table row per
# package in the apt repository, newest version first, in place of the
# <!-- PACKAGES --> line. The rows come from the Packages indexes the repository
# serves, so the page always lists exactly what apt can install.
#
#   scripts/build-landing-page.sh <apt-repo-dir> <template> <output>
set -euo pipefail

if [ $# -ne 3 ]; then
    echo "Usage: $0 <apt-repo-dir> <template> <output>" >&2
    exit 1
fi
apt_dir=$1
template=$2
out=$3

rows=$(mktemp)
trap 'rm -f "$rows"' EXIT

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

sed -e "/<!-- PACKAGES -->/{
r $rows
d
}" "$template" > "$out"
