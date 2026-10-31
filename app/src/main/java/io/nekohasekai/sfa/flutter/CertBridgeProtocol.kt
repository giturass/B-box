package io.nekohasekai.sfa.flutter

internal fun certBridgeFields(output: String): Map<String, String> = output.lineSequence().mapNotNull { line ->
    val fields = line.trim().split('=', limit = 2)
    if (fields.size == 2 && fields[0].matches(Regex("[a-z][a-z0-9_]*"))) fields[0] to fields[1].trim() else null
}.toMap()

internal fun certBridgeFilename(value: String): String {
    require(value.matches(Regex("[0-9a-fA-F]{8}\\.[0-9]{1,4}"))) { "Invalid CertBridge certificate filename" }
    return value
}

internal fun certBridgeCustomFiles(output: String): Set<String> = output.lineSequence()
    .filter { it.startsWith("custom|") }
    .map { certBridgeFilename(it.split('|', limit = 3).getOrElse(1) { "" }) }
    .toSet()

internal fun certificateFingerprint(value: String): String {
    // CertBridge's full OpenSSL fallback only strips the lowercase label;
    // some OpenSSL builds return "SHA256 Fingerprint=" instead.
    val normalized = value.trim()
        .replaceFirst(Regex("^SHA-?256\\s+Fingerprint\\s*=\\s*", RegexOption.IGNORE_CASE), "")
        .replace(":", "").lowercase()
    require(normalized.matches(Regex("[0-9a-f]{64}"))) { "Invalid certificate fingerprint" }
    return normalized
}

internal fun checkCertBridgeIdentity(output: String, expected: String) {
    val fields = certBridgeFields(output)
    require(fields["ok"] == "1" && fields["error"] == null) { "Unable to verify CertBridge certificate" }
    val fingerprint = requireNotNull(fields["fingerprint_sha256"]) { "CertBridge did not return a SHA-256 certificate fingerprint" }
    require(certificateFingerprint(fingerprint) == certificateFingerprint(expected)) {
        "CertBridge certificate fingerprint does not match the B-box CA"
    }
}
