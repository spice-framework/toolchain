module github.com/spice-framework/toolchain

go 1.26.0

toolchain go1.26.5

tool (
	github.com/spice-framework/toolchain/cmd/spice
	github.com/spice-framework/toolchain/cmd/spice-annotation-core
)

require (
	github.com/spice-framework/spice v0.0.0-20260805222830-a2ecd56df246
	golang.org/x/mod v0.38.0
	golang.org/x/sys v0.47.0
	golang.org/x/tools v0.48.0
)

require golang.org/x/sync v0.22.0 // indirect
