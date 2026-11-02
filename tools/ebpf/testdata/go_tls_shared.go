// CI-only c-shared entry keeps the same TLS client reachable in a JNI-style
// Go shared library. Static inspection never calls this exported function.
package main

import "C"

//export RunTLSFixture
func RunTLSFixture() { main() }
