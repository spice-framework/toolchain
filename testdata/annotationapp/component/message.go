// Package component contains the fixture application's generated dependencies.
package component

// @import { Factory as Construct } from "example.com/spice-annotation-fixture/annotation/wiring"

// Message is provided through a third-party annotation contribution.
type Message string

// @Construct
func ProvideMessage() Message {
	return "third-party fixture"
}
