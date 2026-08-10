package diagnostic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetNormalizesSortsAndDefensivelyCopies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	firstLocation := SourceLocation(
		root,
		"app/first.go",
		filepath.Join(root, "app", "first.go"),
		2,
		3,
		7,
	)
	secondLocation := SourceLocation(
		root,
		"app/second file.go",
		filepath.Join(root, "app", "second file.go"),
		4,
		5,
		19,
	)
	version := 4
	fix := SuggestedFix{
		Title:     "Insert comment prefix",
		AppliesTo: &firstLocation,
		Edits: []TextEdit{{
			Location:        secondLocation,
			DocumentVersion: &version,
			NewText:         "// ",
		}},
	}
	second := New(
		Code("Validation", "Unknown Annotation"),
		SeverityError,
		" unknown annotation ",
		secondLocation,
	).WithRelated(RelatedInformation{
		Message:  "definition",
		Location: firstLocation,
	}).WithFixes(fix)
	first := New(
		Code("load", "type"),
		Severity("invalid"),
		"type failure",
		firstLocation,
	)

	set := NewSet(second, first)
	items := set.Items()
	if len(items) != 2 ||
		items[0].Code != "spice.load.type" ||
		items[0].Severity != SeverityError ||
		items[1].Message != "unknown annotation" {
		t.Fatalf("Items() = %#v", items)
	}
	if !strings.Contains(items[1].Location.URI, "second%20file.go") {
		t.Fatalf("URI = %q", items[1].Location.URI)
	}
	if items[1].Location.Display == nil ||
		items[1].Location.Display.Path != "app/second file.go" {
		t.Fatalf("Location = %#v", items[1].Location)
	}

	items[1].Fixes[0].Edits[0].NewText = "changed"
	*items[1].Fixes[0].Edits[0].DocumentVersion = 9
	items[1].Fixes[0].AppliesTo.Path = "changed"
	items[1].Related[0].Message = "changed"
	again := set.Items()
	if again[1].Fixes[0].Edits[0].NewText != "// " ||
		*again[1].Fixes[0].Edits[0].DocumentVersion != 4 ||
		again[1].Fixes[0].AppliesTo.Path != firstLocation.Path ||
		again[1].Related[0].Message != "definition" {
		t.Fatalf("Set was mutated through Items(): %#v", again[1])
	}
}

func TestReportJSONIsDeterministic(t *testing.T) {
	t.Parallel()
	set := NewSet(New(
		Code("application", "missing-root-provider"),
		SeverityError,
		"missing provider",
		SourceLocation("", "main.go", "main.go", 8, 4, 71),
	))
	report := NewReport(false, "verification failed", set)
	first, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) ||
		!bytes.Contains(first, []byte(`"schema": "spice.diagnostics/v1"`)) ||
		!bytes.Contains(first, []byte(`"success": false`)) ||
		!bytes.HasSuffix(first, []byte("\n")) {
		t.Fatalf("JSON() = %s", first)
	}
	report.Diagnostics[0].Message = "changed"
	if set.Items()[0].Message != "missing provider" {
		t.Fatal("report mutated source set")
	}
}

func TestCodeLocationAndErrorFallbacks(t *testing.T) {
	t.Parallel()
	if got := Code("Application Model", "Missing_Root"); got !=
		"spice.application-model.missing-root" {
		t.Fatalf("Code() = %q", got)
	}
	if got := CodeParts("Style", "File", "One Primary Type"); got !=
		"spice.style.file.one-primary-type" {
		t.Fatalf("CodeParts() = %q", got)
	}
	item := New("", "", " broken ", Location{
		Range: Range{
			Start: Position{Line: -1, Column: -1, Offset: -1},
			End:   Position{},
		},
	})
	if item.Code != "spice.compiler.unknown" ||
		item.Severity != SeverityError ||
		item.Location.Range.Start.Line != 1 ||
		item.Location.Range.End.Column != 1 ||
		item.Error() !=
			"<unknown>:1:1: [spice.compiler.unknown] broken" {
		t.Fatalf("New() = %#v, error=%q", item, item.Error())
	}
}

func TestMergeReturnsIndependentSortedSet(t *testing.T) {
	t.Parallel()
	left := NewSet(New(
		Code("z", "last"),
		SeverityWarning,
		"last",
		SourceLocation("", "z.go", "z.go", 1, 1, 0),
	))
	right := NewSet(New(
		Code("a", "first"),
		SeverityHint,
		"first",
		SourceLocation("", "a.go", "a.go", 1, 1, 0),
	))
	merged := Merge(left, right)
	if merged.Len() != 2 ||
		merged.Empty() ||
		merged.Items()[0].Message != "first" {
		t.Fatalf("Merge() = %#v", merged.Items())
	}
	if !NewSet().Empty() {
		t.Fatal("empty set reports non-empty")
	}
}

func BenchmarkNewSet(b *testing.B) {
	items := make([]Diagnostic, 100)
	for index := range items {
		items[index] = New(
			Code("validation", "problem"),
			SeverityError,
			fmt.Sprintf("problem %03d", index),
			SourceLocation(
				"",
				fmt.Sprintf("file-%03d.go", 99-index),
				fmt.Sprintf("file-%03d.go", 99-index),
				index+1,
				1,
				index*10,
			),
		)
	}
	b.ReportAllocs()
	for b.Loop() {
		set := NewSet(items...)
		if set.Len() != len(items) {
			b.Fatalf("Len() = %d", set.Len())
		}
	}
}

func FuzzDiagnosticReportJSON(f *testing.F) {
	f.Add(
		"validation",
		"unknown-annotation",
		"unknown annotation",
		"main.go",
		8,
		3,
	)
	f.Add("", "", "", "", -1, -1)
	f.Fuzz(func(
		t *testing.T,
		stage,
		kind,
		message,
		path string,
		line,
		column int,
	) {
		set := NewSet(New(
			Code(stage, kind),
			SeverityError,
			message,
			SourceLocation("", path, path, line, column, 0),
		))
		content, err := NewReport(false, "fuzz", set).JSON()
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(content) {
			t.Fatalf("invalid JSON: %q", content)
		}
		var report Report
		if err := json.Unmarshal(content, &report); err != nil {
			t.Fatal(err)
		}
		if report.Schema != reportSchema ||
			len(report.Diagnostics) != 1 {
			t.Fatalf("report = %#v", report)
		}
	})
}
