package annotationhost

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
)

func TestValidateDescriptorRequiresMatchingDeclaredRegistration(t *testing.T) {
	t.Parallel()
	definition := sdk.Definition{Name: "fixture.Echo"}
	provenance := annotation.ModuleProvenance{
		Path: "example.com/annotationfixture", Version: "v1.0.0",
	}

	var nilClient *Client
	if err := nilClient.ValidateDescriptor("example.com/annotationfixture/annotation", "Echo", definition, provenance); err == nil ||
		!strings.Contains(err.Error(), "tool client is nil") {
		t.Fatalf("nil ValidateDescriptor() error = %v", err)
	}

	client := &Client{
		provenance: PackageIdentity{
			Path: fixtureTool,
			Module: ModuleIdentity{
				Path: "example.com/annotationfixture", Version: "v1.0.0",
			},
		},
		descriptorPackages: []string{"example.com/annotationfixture/annotation"},
		handlers: map[string]protocol.Handler{
			"other": {
				Descriptor: sdk.Symbol{
					Package: "example.com/annotationfixture/annotation",
					Name:    "Other",
				},
			},
		},
	}

	changed := provenance
	changed.Version = "v1.1.0"
	if err := client.ValidateDescriptor(
		"example.com/annotationfixture/annotation", "Echo", definition, changed,
	); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched module error = %v", err)
	}
	if err := client.ValidateDescriptor(
		"example.com/annotationfixture/annotation", "Echo", definition, provenance,
	); err == nil || !strings.Contains(err.Error(), "missing tool registration") {
		t.Fatalf("missing registration error = %v", err)
	}

	client.handlers["echo"] = protocol.Handler{Descriptor: sdk.Symbol{
		Package: "example.com/annotationfixture/annotation", Name: "Echo",
	}}
	if err := client.ValidateDescriptor(
		"example.com/annotationfixture/annotation", "Echo", definition, provenance,
	); err != nil {
		t.Fatalf("matching ValidateDescriptor() error = %v", err)
	}
}

func TestManagerCloseAllOwnedProcesses(t *testing.T) {
	root := writeToolFixture(t)
	manager := NewManager()
	client, err := manager.Client(context.Background(), Config{
		Root: root, ToolPath: fixtureTool,
	})
	if err != nil {
		t.Fatalf("Client() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if client.healthy() || len(manager.clients) != 0 {
		t.Fatalf("manager retained closed process: healthy=%t clients=%d", client.healthy(), len(manager.clients))
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatalf("Close(empty) error = %v", err)
	}
}

func TestManagerCloseCancelsAndRemovesProcesses(t *testing.T) {
	root := writeToolFixture(t)
	manager := NewManager()
	client, err := manager.Client(context.Background(), Config{
		Root: root, ToolPath: fixtureTool,
	})
	if err != nil {
		t.Fatalf("Client() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = manager.Close(cancelled)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Close(cancelled) error = %v", err)
	}
	if client.healthy() || len(manager.clients) != 0 {
		t.Fatalf("cancelled close retained process: healthy=%t clients=%d", client.healthy(), len(manager.clients))
	}

	var nilManager *Manager
	if err := nilManager.Close(context.Background()); err != nil {
		t.Fatalf("nil manager Close() error = %v", err)
	}
}
