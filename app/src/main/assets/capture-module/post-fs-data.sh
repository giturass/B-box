#!/system/bin/sh
# ProxyPin-style system and Conscrypt mounts; see NOTICE and LICENSE.
# Original author: firdausmntp. Modified for B-box on 2026-10-31 (GPL-2.0).
MODDIR=${0%/*}
. "$MODDIR/ca-store.sh" || exit 1
: > "$MODDIR/mount.log"
ca_log "Preparing B-box CA stores during post-fs-data"
ca_mount_all
