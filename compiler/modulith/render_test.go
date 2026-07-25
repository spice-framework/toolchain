package modulith

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestRenderProducesDeterministicPortableModuleCanvases(t *testing.T) {
	root := writeModule(t, map[string]string{
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/shop/payments", "example.com/shop/payments::spi"])
package orders
`,
		"orders/use/use.go": `package use

import (
	"example.com/shop/payments"
	paymentspi "example.com/shop/payments/spi"
)

var (
	Payment payments.Payment
	Request paymentspi.Request
)
`,
		"payments/package.go": `// Package payments owns payments.
//
// @Module
package payments

type Payment struct{}
`,
		"payments/spi/package.go": `// Package spi exposes payment contracts.
//
// @NamedInterface("spi")
package spi

type Request struct{}
`,
		"shared/shared.go": "package shared\n",
	})
	model := buildModel(t, root)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}

	for _, format := range []Format{FormatJSON, FormatMermaid, FormatPlantUML} {
		var first []byte
		for iteration := range 20 {
			content, err := Render(model, format)
			if err != nil {
				t.Fatalf("Render(%s) error = %v", format, err)
			}
			if iteration == 0 {
				first = content
			} else if !bytes.Equal(content, first) {
				t.Fatalf("Render(%s) changed bytes at iteration %d", format, iteration)
			}
		}
		if bytes.Contains(first, []byte(root)) {
			t.Fatalf("Render(%s) contains absolute root %q", format, root)
		}
	}

	jsonContent, err := Render(model, FormatJSON)
	if err != nil {
		t.Fatalf("Render(json) error = %v", err)
	}
	var document moduleGraphDocument
	if decodeErr := json.Unmarshal(jsonContent, &document); decodeErr != nil {
		t.Fatalf("json.Unmarshal() error = %v", decodeErr)
	}
	if document.Schema != documentSchema || len(document.Modules) != 2 || len(document.Edges) != 2 {
		t.Fatalf("JSON document = %#v", document)
	}
	orders := document.Modules[0]
	if orders.ID != "example.com/shop/orders" ||
		!slices.Equal(orders.ObservedDependencies, []string{
			"example.com/shop/payments",
			"example.com/shop/payments::spi",
		}) {
		t.Fatalf("orders canvas = %#v", orders)
	}
	if !slices.Equal(document.UnassignedPackages, []string{"example.com/shop/shared"}) {
		t.Fatalf("unassigned packages = %v", document.UnassignedPackages)
	}

	mermaid, err := Render(model, FormatMermaid)
	if err != nil {
		t.Fatalf("Render(mermaid) error = %v", err)
	}
	for _, expected := range []string{
		"flowchart LR",
		`M0["example.com/shop/orders"]`,
		`M1["example.com/shop/payments"]`,
		`u0["unassigned: example.com/shop/shared"]:::unassigned`,
		"M0 -->|default, spi| M1",
	} {
		if !strings.Contains(string(mermaid), expected) {
			t.Fatalf("Mermaid output missing %q:\n%s", expected, mermaid)
		}
	}

	plantUML, err := Render(model, FormatPlantUML)
	if err != nil {
		t.Fatalf("Render(plantuml) error = %v", err)
	}
	for _, expected := range []string{
		"@startuml",
		`component "example.com/shop/orders" as M0`,
		`component "unassigned: example.com/shop/shared" as U0 #LightGray`,
		"M0 --> M1 : default, spi",
		"@enduml",
	} {
		if !strings.Contains(string(plantUML), expected) {
			t.Fatalf("PlantUML output missing %q:\n%s", expected, plantUML)
		}
	}
}

func TestRenderRejectsUnknownFormatAndUsesEmptyArrays(t *testing.T) {
	if _, err := Render(Model{}, Format("dot")); err == nil ||
		!strings.Contains(err.Error(), "expected json, mermaid, or plantuml") {
		t.Fatalf("Render(unknown) error = %v", err)
	}
	content, err := Render(Model{}, FormatJSON)
	if err != nil {
		t.Fatalf("Render(empty JSON) error = %v", err)
	}
	for _, expected := range []string{`"modules": []`, `"edges": []`, `"cycles": []`, `"unassigned_packages": []`} {
		if !bytes.Contains(content, []byte(expected)) {
			t.Fatalf("empty JSON missing %q:\n%s", expected, content)
		}
	}
}
