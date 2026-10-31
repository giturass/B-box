#!/bin/sh
# CI only: real tmpfs/bind tests on fixture directories inside private mount/PID
# namespaces. This never installs a module or changes an Android trust store.
set -eu
[ "${CAPTURE_MODULE_CI:-}" = 1 ] && [ "$$" -eq 1 ] || {
    echo 'Run only in CI via unshare --mount --pid --fork --mount-proc.' >&2
    exit 1
}

repo_root=$PWD
mkdir -p "$HOME/tmp"
fixture_dir=$(mktemp -d "$HOME/tmp/bbox-ca-test.XXXXXX")
peer_pid=
cleanup() {
    set +e
    if [ -n "$peer_pid" ]; then kill "$peer_pid"; wait "$peer_pid"; fi
    for path in "$fixture_dir/target" "$fixture_dir/module"/.ca-*; do
        while mountpoint -q "$path"; do umount "$path" || break; done
    done
    rm -rf "$fixture_dir"
}
trap cleanup EXIT

# Match the BusyBox applets used by Magisk/KernelSU, including mount inside
# nsenter. Linux mount tests cannot exercise Android SELinux policies.
mkdir "$fixture_dir/bin"
busybox_path=$(command -v busybox)
for applet in mount umount mountpoint nsenter; do
    ln -s "$busybox_path" "$fixture_dir/bin/$applet"
done
PATH="$fixture_dir/bin:$PATH"
export PATH
getenforce() { echo Disabled; }
pidof() { return 0; }

MODDIR="$fixture_dir/module"
mkdir -p "$MODDIR/system/etc/security/cacerts" "$fixture_dir/target" "$MODDIR/.ca-system"
cert="$MODDIR/system/etc/security/cacerts/6187b673.0"
cp "$repo_root/app/src/test/resources/capture-ca.pem" "$cert"
printf 'original zero\n' > "$fixture_dir/target/6187b673.0"
printf 'original one\n' > "$fixture_dir/target/6187b673.1"
printf 'unrelated root\n' > "$fixture_dir/target/aaaaaaaa.0"
cp -R "$fixture_dir/target" "$fixture_dir/original"

# Start the destination namespace BEFORE the staging tmpfs exists. A plain
# source path after nsenter would bind an empty directory over its CA store.
/usr/bin/unshare --mount --propagation private sh -c 'touch "$1"; exec sleep 120' sh "$fixture_dir/peer-ready" &
peer_pid=$!
attempts=0
while [ ! -e "$fixture_dir/peer-ready" ]; do
    sleep 0.1
    attempts=$((attempts + 1))
    [ "$attempts" -lt 50 ]
done

. "$repo_root/app/src/main/assets/capture-module/ca-store.sh"
ca_mount_store "$fixture_dir/target" system
cmp "$cert" "$fixture_dir/target/6187b673.2"
for original in "$fixture_dir/original"/*; do
    cmp "$original" "$fixture_dir/target/${original##*/}"
done
nsenter --mount="/proc/$peer_pid/ns/mnt" -- test ! -e "$fixture_dir/target/6187b673.2"
ca_mount_namespace "$MODDIR/.ca-system" "$fixture_dir/target" "$peer_pid"
nsenter --mount="/proc/$peer_pid/ns/mnt" -- cmp "$cert" "$fixture_dir/target/6187b673.2"
for original in "$fixture_dir/original"/*; do
    nsenter --mount="/proc/$peer_pid/ns/mnt" -- cmp "$original" "$fixture_dir/target/${original##*/}"
done

# Re-running boot/service helpers must preserve originals and avoid duplicates.
ca_mount_store "$fixture_dir/target" system
[ ! -e "$fixture_dir/target/6187b673.3" ]
mkdir "$fixture_dir/copied"
ca_copy_store "$fixture_dir/target" "$fixture_dir/copied" "$cert"
[ ! -e "$fixture_dir/copied/6187b673.3" ]
cmp "$cert" "$fixture_dir/copied/6187b673.2"

# Partial copy or an empty original store must never mount a replacement.
cp() {
    [ "$2" != "$fixture_dir/original/6187b673.1" ] || return 1
    command cp "$@"
}
if ca_mount_store "$fixture_dir/original" failed; then
    echo 'Partial CA copy unexpectedly succeeded' >&2
    exit 1
fi
! mountpoint -q "$fixture_dir/original"
[ ! -e "$fixture_dir/original/6187b673.2" ]
cmp "$fixture_dir/target/6187b673.0" "$fixture_dir/original/6187b673.0"
cmp "$fixture_dir/target/6187b673.1" "$fixture_dir/original/6187b673.1"
[ ! -e "$MODDIR/.ca-failed" ]
mkdir "$fixture_dir/empty"
if ca_mount_store "$fixture_dir/empty" empty; then
    echo 'Empty CA store unexpectedly accepted' >&2
    exit 1
fi
! mountpoint -q "$fixture_dir/empty"
[ ! -e "$MODDIR/.ca-empty" ]
echo 'CA store preservation, collisions, repeat mounts, namespace isolation and copy failures passed.'
