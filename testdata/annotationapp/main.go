package main

import (
	"os"

	spiceapp "example.com/spice-annotation-app/internal/spicegen/spice_annotation_app"
)

// @import { Application } from "github.com/spice-framework/spice/annotation/core"
// @import { Factory as Construct } from "example.com/spice-annotation-fixture/annotation/wiring"
// @import * as fixture from "example.com/spice-annotation-fixture/annotation/policy"

// @fixture.Policy(mode="strict")
type Message string

// @Application
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}
