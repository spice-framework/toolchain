package annotationhost

import (
	"context"
	"testing"
	"time"
)

func TestManagerReusesWorkspaceToolAndClosesIt(t *testing.T) {
	root := writeToolFixture(t)
	manager := NewManager()
	first, err := manager.Client(context.Background(), Config{
		Root:     root,
		ToolPath: fixtureTool,
	})
	if err != nil {
		t.Fatalf("Client(first) error = %v", err)
	}
	second, err := manager.Client(context.Background(), Config{
		Root:     root,
		ToolPath: fixtureTool,
	})
	if err != nil {
		t.Fatalf("Client(second) error = %v", err)
	}
	if first != second {
		t.Fatal("Client() did not reuse the persistent process")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.CloseWorkspace(ctx, root); err != nil {
		t.Fatalf("CloseWorkspace() error = %v", err)
	}
	if first.healthy() {
		t.Fatal("client remains healthy after CloseWorkspace")
	}
}
