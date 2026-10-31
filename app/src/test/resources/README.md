# Public CA fixture

`capture-ca.pem` is the public **ISRG Root X1** certificate, distributed by
[Let's Encrypt](https://letsencrypt.org/certificates/), copied from Android's
`6187b673.0` system trust-store entry. It contains no private key and is used
only by JVM tests; it is not an application asset or a module default.

Reference values from `openssl x509 -subject_hash_old -fingerprint -sha256`:

- Subject hash: `6187b673`
- SHA-256: `96bcec06264976f37460779acf28c5a7cfe8a3c0aae11a8ffcee05c0bddf08c6`
