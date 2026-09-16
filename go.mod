module github.com/spice-framework/toolchain

go 1.26.0

toolchain go1.26.5

tool (
	github.com/spice-framework/toolchain/cmd/spice
	github.com/spice-framework/toolchain/cmd/spice-annotation-core
	github.com/spice-framework/toolchain/cmd/spicestyle
)

require (
	github.com/spice-framework/spice v0.1.0-preview.4
	golang.org/x/mod v0.41.0
	golang.org/x/sys v0.48.0
	golang.org/x/tools v0.50.0
)

require golang.org/x/sync v0.23.0 // indirect
