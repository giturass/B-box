#!/system/bin/sh
# B-box installer, adapted from ProxyPin on 2026-10-31 (GPL-2.0).
# See NOTICE and LICENSE for upstream attribution.
[ "$BOOTMODE" = true ] || abort "Install this ZIP from Magisk or KernelSU."
[ "$API" -ge 24 ] || abort "B-box requires Android 7.0 or newer."

set -- "$MODPATH"/system/etc/security/cacerts/*.0
[ "$#" -eq 1 ] && [ -f "$1" ] || abort "Expected exactly one B-box public CA."
grep -q '^-----BEGIN CERTIFICATE-----$' "$1" || abort "Invalid public CA."

# Scripts mount both stores themselves, so KernelSU needs no system-mount
# metamodule. Avoid overwriting an existing hash.0 before collision handling.
touch "$MODPATH/skip_mount"
set_perm_recursive "$MODPATH" 0 0 0755 0644
for script in post-fs-data.sh service.sh ca-store.sh; do
    set_perm "$MODPATH/$script" 0 0 0755
done
ui_print "B-box CA module installed. Reboot the device."
ui_print "Then open B-box > Requests > CA certificate > Check system trust."
