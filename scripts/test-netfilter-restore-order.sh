#!/usr/bin/env bash
# Unit test: lsh-agent-netfilter-restore.service must be ordered before
# ufw.service, both as install.sh writes it and on hosts that got an older copy
# (fixed by the package postinst with a drop-in). No Docker, no root.
#
#   scripts/test-netfilter-restore-order.sh
#
# Why: the unit restores /etc/iptables/rules.v{4,6} with --noflush, which also
# applies the built-in chain policies in those files. Restored after ufw, a
# policy-only rules.v6 (":INPUT ACCEPT") resets ufw's DROP policy and the
# default-deny is gone.
set -euo pipefail

root=$(dirname "$(dirname "$(realpath "$0")")")
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0
ok() { echo "ok   $1"; }
ko() { echo "FAIL $1"; fail=1; }
before_has_ufw() { grep -qE '^Before=(.*[[:space:]])?ufw\.service([[:space:]]|$)' "$1"; }

# 1. The unit install.sh writes.
unit=$(sed -n "/lsh-agent-netfilter-restore.service << 'EOF'$/,/^EOF$/{//!p}" "$root/install.sh")
[ -n "$unit" ] || { echo "FAIL could not extract the unit from install.sh" >&2; exit 1; }
printf '%s\n' "$unit" > "$work/install-sh.service"
if before_has_ufw "$work/install-sh.service"; then ok "install.sh unit is ordered Before=ufw.service"; else ko "install.sh unit is not ordered before ufw.service"; fi

# 2. The postinst drop-in for hosts with an older copy.
func=$(sed -n '/^order_netfilter_restore_before_ufw() {$/,/^}$/p' "$root/packaging/scripts/postinst")
[ -n "$func" ] || { echo "FAIL could not extract order_netfilter_restore_before_ufw from postinst" >&2; exit 1; }

postinst_case() { # name, unit-content (or "absent"), want-dropin yes|no
    local name=$1 content=$2 want=$3 u="$work/$1/lsh-agent-netfilter-restore.service" got
    mkdir -p "$work/$name"
    [ "$content" = absent ] || printf '%s\n' "$content" > "$u"
    # shellcheck disable=SC2034  # read by the extracted postinst function
    ( NETFILTER_RESTORE_UNIT="$u"; eval "$func"; order_netfilter_restore_before_ufw > /dev/null )
    if [ -f "$u.d/10-before-ufw.conf" ]; then got=yes; else got=no; fi
    if [ "$got" != "$want" ]; then ko "postinst/$name: drop-in=$got (want $want)"; return; fi
    if [ "$want" = yes ] && ! grep -qx 'Before=ufw.service' "$u.d/10-before-ufw.conf"; then ko "postinst/$name: drop-in lacks Before=ufw.service"; return; fi
    ok "postinst/$name: drop-in=$got"
}

old=$(printf '%s\n' "$unit" | sed 's/^Before=network-pre.target shutdown.target ufw.service$/Before=network-pre.target shutdown.target/')
grep -q '^Before=network-pre.target shutdown.target$' <<< "$old" || { echo "FAIL could not derive the pre-fix unit" >&2; exit 1; }

postinst_case no-unit      absent  no
postinst_case old-unit     "$old"  yes
postinst_case fixed-unit   "$unit" no

exit "$fail"
