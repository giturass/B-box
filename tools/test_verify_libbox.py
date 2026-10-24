import io
from pathlib import Path
import unittest
from unittest.mock import patch
from zipfile import ZipFile

import verify_libbox as verifier


class VerifyLibboxTest(unittest.TestCase):
    def metadata(self, extra_module=""):
        modules = verifier.REQUIRED_MODULES | ({extra_module} if extra_module else set())
        return "\n".join([
            *(f"dep\t{module}\tv1.0.0" for module in sorted(modules)),
            "build\t-tags=" + ",".join(sorted(verifier.REQUIRED_TAGS)),
            "build\tGOOS=android",
            "build\tGOARCH=arm64",
        ])

    def test_preserved_features_pass(self):
        self.assertEqual(verifier.verify_metadata(self.metadata()), verifier.REQUIRED_TAGS)

    def test_pty_module_and_submodules_rejected(self):
        for module in ("github.com/creack/pty", "github.com/creack/pty/subpackage"):
            with self.subTest(module=module), self.assertRaises(ValueError):
                verifier.verify_metadata(self.metadata(module))

    def test_tailssh_dependency_rejected(self):
        with patch.object(verifier.subprocess, "check_output", return_value="github.com/sagernet/sing-box/protocol/tailscale/tailssh\n"):
            with self.assertRaises(ValueError):
                verifier.verify_source(Path("."), verifier.REQUIRED_TAGS)

    def test_tailssh_and_pty_native_symbols_rejected(self):
        for symbol in (
            b"github.com/sagernet/sing-box/protocol/tailscale/tailssh.StartPtyProcess\x00",
            b"github.com/creack/pty.StartWithAttrs\x00",
        ):
            with self.subTest(symbol=symbol), self.assertRaises(ValueError):
                verifier.verify_native(symbol)

    def test_shared_types_and_error_placeholders_allowed(self):
        verifier.verify_native(
            b"github.com/sagernet/sing-box/option.TailscaleEndpointOptions\x00"
            b"github.com/sagernet/sing-box/daemon.TailscalePeer\x00"
            b"Tailscale is not included in this build"
        )

    def test_package_name_boundaries(self):
        verifier.verify_native(b"github.com/sagernet/sing-box/protocol/torus.Start\x00")

    def check_bindings(self, class_name=None, libbox=b"preserved bindings"):
        jar_data = io.BytesIO()
        with ZipFile(jar_data, "w") as jar:
            jar.writestr("io/nekohasekai/libbox/Libbox.class", libbox)
            if class_name:
                jar.writestr("io/nekohasekai/libbox/" + class_name + ".class", b"class")
        aar_data = io.BytesIO()
        with ZipFile(aar_data, "w") as aar:
            aar.writestr("classes.jar", jar_data.getvalue())
        with ZipFile(aar_data) as aar:
            verifier.verify_bindings(aar)

    def test_removed_binding_classes_rejected(self):
        for name in ("TailscalePeer", "TaildropSendSession"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.check_bindings(class_name=name)

    def test_native_session_exports_rejected(self):
        for name in (b"openNativeShellSession", b"openNativePipeSession"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.check_bindings(libbox=name)

    def test_preserved_remote_binding_allowed(self):
        self.check_bindings(class_name="OpenVPNStatusUpdate")


if __name__ == "__main__":
    unittest.main()
