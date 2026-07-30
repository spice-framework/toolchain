// @import { Application } from "github.com/StevenBuglione/spice/annotation/core"
// @import { Module } from "github.com/StevenBuglione/spice/annotation/modulith"

// Package spiceapp owns the generated production Spice CLI application graph.
//
// @Module(allowedDependencies=["github.com/StevenBuglione/spice/internal/cli"])
package spiceapp

import (
	_ "github.com/StevenBuglione/spice/internal/autoconfigure"
	"github.com/StevenBuglione/spice/internal/cli"
)

// Spice defines the production CLI root. Its body is never executed during
// analysis or at runtime.
//
// @Application
func Spice(*cli.Command) {
	panic("Spice application marker bodies must never execute")
}
