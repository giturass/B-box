#!/system/bin/sh
MODDIR=${0%/*}
# Keep this boot's diagnostics bounded to one boot rather than accumulating.
: > "$MODDIR/mount.log"
# This stage blocks Android boot; APEX/Zygote readiness belongs to service.sh.
sh "$MODDIR/inject.sh" --early >> "$MODDIR/mount.log" 2>&1
