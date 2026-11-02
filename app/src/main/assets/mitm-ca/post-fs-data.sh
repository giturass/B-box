#!/system/bin/sh
MODDIR=${0%/*}
# Keep this boot's diagnostics bounded to one boot rather than accumulating.
: > "$MODDIR/mount.log"
sh "$MODDIR/inject.sh"
