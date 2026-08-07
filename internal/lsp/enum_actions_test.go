package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/diagnostic"
	compilerservice "github.com/spice-framework/toolchain/compiler/service"
)

func TestEnumHelperCodeActionGeneratesVersionedHelpersAndImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "order_status.go")
	content := []byte(`package orders

// @Enum
type OrderStatus string

const (
	OrderStatusPending OrderStatus = "pending"
	OrderStatusPaid OrderStatus = "paid"
)
`)
	source := document{
		uri:     fileURIFromPath(t, path),
		path:    path,
		version: 7,
		content: content,
	}
	enum := compilerservice.Enum{
		Name:       "OrderStatus",
		TypeID:     "example.com/shop/orders.OrderStatus",
		Underlying: "string",
		Location: diagnostic.SourceLocation(
			filepath.Dir(path),
			path,
			path,
			4,
			6,
			31,
		),
		Members: []compilerservice.EnumMember{
			{Name: "OrderStatusPending", Value: `"pending"`},
			{Name: "OrderStatusPaid", Value: `"paid"`},
		},
	}
	action, available := enumHelperCodeAction(source, enum)
	if !available ||
		action.Title != "Generate enum helpers for OrderStatus" ||
		action.Kind != "refactor.rewrite" ||
		action.Edit == nil ||
		len(action.Edit.DocumentChanges) != 1 {
		t.Fatalf("enumHelperCodeAction() = %+v, %t", action, available)
	}
	change := action.Edit.DocumentChanges[0]
	if change.TextDocument.Version != 7 ||
		change.TextDocument.URI != source.uri ||
		len(change.Edits) != 2 ||
		change.Edits[0].NewText != "\n\nimport \"fmt\"" {
		t.Fatalf("document change = %+v", change)
	}
	helpers := change.Edits[1].NewText
	for _, expected := range []string{
		"func ParseOrderStatus(value string) (OrderStatus, error)",
		"fmt.Errorf",
		"func (value OrderStatus) String() string",
		"return string(value)",
		"func (value OrderStatus) Valid() bool",
		"case OrderStatusPending, OrderStatusPaid:",
	} {
		if !strings.Contains(helpers, expected) {
			t.Fatalf("generated helpers missing %q:\n%s", expected, helpers)
		}
	}
}

func TestEnumHelperCodeActionGeneratesOnlyMissingHelpers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.go")
	content := []byte(`package sample

import format "fmt"

type State int

const StateReady State = 1

func ParseState(value int) (State, error) { return State(value), nil }

func (value State) Valid() bool { return value == StateReady }
`)
	action, available := enumHelperCodeAction(document{
		uri:     fileURIFromPath(t, path),
		path:    path,
		version: 2,
		content: content,
	}, compilerservice.Enum{
		Name:       "State",
		Underlying: "int",
		Members: []compilerservice.EnumMember{
			{Name: "StateReady", Value: "1"},
		},
	})
	if !available || action.Edit == nil {
		t.Fatalf("enumHelperCodeAction() = %+v, %t", action, available)
	}
	edits := action.Edit.DocumentChanges[0].Edits
	if len(edits) != 1 ||
		!strings.Contains(edits[0].NewText, "func (value State) String() string") ||
		!strings.Contains(edits[0].NewText, "format.Sprint(int(value))") ||
		strings.Contains(edits[0].NewText, "ParseState") ||
		strings.Contains(edits[0].NewText, "Valid()") {
		t.Fatalf("edits = %+v", edits)
	}
}

func TestEnumHelperCodeActionSkipsCompleteOrUnsafeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.go")
	complete := []byte(`package sample

type State string

func ParseState(value string) (State, error) { return State(value), nil }
func (value State) String() string { return string(value) }
func (value State) Valid() bool { return true }
`)
	if _, available := enumHelperCodeAction(document{
		path:    path,
		content: complete,
	}, compilerservice.Enum{Name: "State", Underlying: "string"}); available {
		t.Fatal("complete enum unexpectedly offered helper generation")
	}
	unsafe := []byte("package sample\n\nimport _ \"fmt\"\n\ntype State string\n")
	if _, available := enumHelperCodeAction(document{
		path:    path,
		content: unsafe,
	}, compilerservice.Enum{Name: "State", Underlying: "string"}); available {
		t.Fatal("blank fmt import unexpectedly offered unsafe helper generation")
	}
}

func fileURIFromPath(t *testing.T, path string) string {
	t.Helper()
	uri, err := fileURI(path)
	if err != nil {
		t.Fatalf("fileURI(%s) error = %v", path, err)
	}
	return uri
}
