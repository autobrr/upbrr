#!/usr/bin/env bash
# Copyright (c) 2025-2026, Audionut and the autobrr contributors.
# SPDX-License-Identifier: GPL-2.0-or-later
set -euo pipefail

if [[ $(uname -s) != Linux || $# -eq 0 ]]; then
  echo 'usage (Linux): bash scripts/ci/test-loopback.sh command [args...]' >&2
  exit 2
fi

setup=$(cat <<'BASH'
set -euo pipefail
test_uid=$1 test_gid=$2 test_home=$3 test_path=$4
shift 4

ip link set lo up
# Route prohibited destinations into the output chain so attempts are counted
# even though the namespace has no external interface.
ip -4 route add local 0.0.0.0/0 dev lo src 127.0.0.1 table local
ip -6 route add local ::/0 dev lo src ::1 table local
nft -f - <<'NFT'
table inet test_network {
  counter blocked {}
  chain output {
    type filter hook output priority filter; policy drop;
    oifname "lo" ip daddr 127.0.0.0/8 accept
    oifname "lo" ip6 daddr ::1 accept
    counter name blocked reject
  }
}
NFT

# Keep DNS inside the namespace, including on hosts that use a resolver daemon.
resolver_dir=$(mktemp -d)
trap 'rm -f -- "$resolver_dir/resolv.conf" "$resolver_dir/nsswitch.conf"; rmdir -- "$resolver_dir"' EXIT
printf 'nameserver 192.0.2.53\noptions attempts:1 timeout:1\n' > "$resolver_dir/resolv.conf"
sed 's/^hosts:.*/hosts: files dns/' /etc/nsswitch.conf > "$resolver_dir/nsswitch.conf"
chmod 644 "$resolver_dir/resolv.conf" "$resolver_dir/nsswitch.conf"
mount --bind "$resolver_dir/resolv.conf" /etc/resolv.conf
mount --bind "$resolver_dir/nsswitch.conf" /etc/nsswitch.conf
if [[ -S /run/nscd/socket ]]; then
  mount --bind /dev/null /run/nscd/socket
fi

status=0
setpriv --reuid "$test_uid" --regid "$test_gid" --init-groups --bounding-set=-all --no-new-privs \
  env HOME="$test_home" PATH="$test_path" "$@" || status=$?

blocked=$(nft list counter inet test_network blocked | awk '$1 == "packets" {print $2}')
if [[ ! $blocked =~ ^[0-9]+$ ]]; then
  echo 'test-loopback: could not read network counter' >&2
  exit 2
fi
if ((blocked != 0)); then
  echo "test-loopback: blocked $blocked non-loopback packet(s)" >&2
  exit 1
fi
exit "$status"
BASH
)

exec sudo -n -E unshare --net --mount --propagation private --fork --kill-child \
  bash -c "$setup" test-loopback "$(id -u)" "$(id -g)" "$HOME" "$PATH" "$@"
