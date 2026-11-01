module github.com/giturass/B-box/capture

go 1.25.0

require (
	github.com/cilium/ebpf v0.17.3
	github.com/ulikunitz/xz v0.5.15
	golang.org/x/sys v0.43.0
)

// Android fork used by stackplz; the revision is pinned by eDBG's submodule.
// See README.md for the fixed source references and dependency provenance.
replace github.com/cilium/ebpf => github.com/SeeFlowerX/ebpf v0.0.0-20240104082000-61cbc4a37e82

require golang.org/x/exp v0.0.0-20230224173230-c95f2b4c22f2 // indirect
