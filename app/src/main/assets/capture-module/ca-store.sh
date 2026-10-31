#!/system/bin/sh
# Adapted from ProxyPin Certificate Installer d4ff2b93e515a074ee2deab48c56fd6473213fb6.
# Original author: firdausmntp. Modified for B-box on 2026-10-31 (GPL-2.0).
# See NOTICE and LICENSE. This file defines helpers; it does not install a CA.

ca_log() {
    printf '%s\n' "$*" >> "$MODDIR/mount.log"
}

# Prepare a complete copy before mounting anything over a system trust store.
# Keep existing hash.N certificates when our CA has the same subject hash.
ca_copy_store() (
    target=$1
    stage=$2
    cert=$3
    count=0
    for original in "$target"/*; do
        [ -f "$original" ] || continue
        cp -f "$original" "$stage/" || exit 1
        count=$((count + 1))
    done
    [ "$count" -gt 0 ] || exit 1
    name=${cert##*/}
    hash=${name%.0}
    index=0
    while [ -e "$stage/$hash.$index" ] || [ -L "$stage/$hash.$index" ]; do
        cmp -s "$cert" "$stage/$hash.$index" && exit 0
        index=$((index + 1))
        [ "$index" -lt 10000 ] || exit 1
    done
    cp -f "$cert" "$stage/$hash.$index"
)

ca_set_context() (
    case "$(getenforce)" in
        Disabled) exit 0 ;;
        Enforcing|Permissive) ;;
        *) exit 1 ;;
    esac
    context=$(ls -Zd "$1" | awk '{print $1}')
    [ -n "$context" ] && [ "$context" != '?' ] || context=u:object_r:system_file:s0
    chcon -R "$context" "$2"
)

ca_mount_namespace() {
    [ -e "/proc/$3/ns/mnt" ] || return 0
    # Resolve the prepared tmpfs through OUR namespace. The same path in the
    # destination namespace may still be an empty directory underneath it.
    nsenter --mount="/proc/$3/ns/mnt" -- mount --bind "/proc/$$/root$1" "$2" || \
        ca_log "Unable to mount $2 in process $3"
}

ca_mount_store() (
    target=$1
    stage="$MODDIR/.ca-$2"
    [ -d "$target" ] || exit 0
    set -- "$MODDIR"/system/etc/security/cacerts/*.0
    [ "$#" -eq 1 ] && [ -f "$1" ] || { ca_log "Expected one B-box public CA"; exit 1; }
    cert=$1

    if ! mountpoint -q "$stage"; then
        [ ! -L "$stage" ] || exit 1
        mkdir -p "$stage" || exit 1
        mount -t tmpfs -o mode=0755 tmpfs "$stage" || exit 1
        if ! ca_copy_store "$target" "$stage" "$cert" ||
            ! chown -R 0:0 "$stage" || ! chmod 0644 "$stage"/* ||
            ! ca_set_context "$target" "$stage"; then
            ca_log "Certificate copy failed; keeping $target unchanged"
            umount "$stage"
            rmdir "$stage"
            exit 1
        fi
    fi

    mount --bind "$stage" "$target" || { ca_log "Unable to mount $target"; exit 1; }
    ca_mount_namespace "$stage" "$target" 1
    for pid in $(pidof zygote) $(pidof zygote64); do
        ca_mount_namespace "$stage" "$target" "$pid"
    done
    # post-fs-data runs before apps; service.sh also covers existing processes.
    for pid in $(pidof io.ericlee.bfa) $(pidof com.android.settings); do
        ca_mount_namespace "$stage" "$target" "$pid"
    done
    ca_log "Mounted B-box public CA at $target"
)

ca_mount_all() {
    ca_mount_store /system/etc/security/cacerts system
    ca_mount_store /apex/com.android.conscrypt/cacerts apex
}
