#!/system/bin/sh
# Repeat namespace mounts after Zygote and the app processes exist.
# Original author: firdausmntp. Modified for B-box on 2026-10-31 (GPL-2.0).
MODDIR=${0%/*}
. "$MODDIR/ca-store.sh" || exit 1
count=0
while [ "$(getprop sys.boot_completed)" != 1 ] && [ "$count" -lt 60 ]; do
    sleep 1
    count=$((count + 1))
done
ca_log "Refreshing B-box CA namespaces after boot"
ca_mount_all
