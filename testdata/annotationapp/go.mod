module example.com/spice-annotation-app

go 1.26.0

toolchain go1.26.5

tool (
	example.com/spice-annotation-fixture/cmd/spice-annotations
	github.com/spice-framework/toolchain/cmd/spice
	github.com/spice-framework/toolchain/cmd/spice-annotation-core
)

require github.com/spice-framework/spice v0.1.0-preview.1.0.20260806200749-524424a04df0

require (
	example.com/spice-annotation-fixture v0.0.0 // indirect
	github.com/spice-framework/toolchain v0.0.0 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)

replace example.com/spice-annotation-fixture => ../annotationfixture

replace github.com/spice-framework/toolchain => ../..
