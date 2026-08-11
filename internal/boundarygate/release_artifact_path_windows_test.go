//go:build windows

package boundarygate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReleaseArtifactDirectoryCanonicalizesWindowsRunnerSeparators(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "native", input: `C:\verified\subjects`, want: `C:\verified\subjects`},
		{
			name:  "GitHub runner",
			input: `D:\a\_temp/go-distribution-release-verified`,
			want:  `D:\a\_temp\go-distribution-release-verified`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			actual, err := normalizeReleaseArtifactDirectory(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if actual != test.want {
				t.Fatalf("normalized directory = %q, want %q", actual, test.want)
			}
		})
	}
}

func TestReleaseArtifactDirectoryRejectsWindowsPathMutations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "empty"},
		{name: "UNC native", value: `\\server\share\subjects`},
		{name: "UNC slashes", value: `//server/share/subjects`},
		{name: "device native", value: `\\?\C:\verified\subjects`},
		{name: "device slashes", value: `//?/C:/verified/subjects`},
		{name: "DOS device native", value: `\\.\C:\verified\subjects`},
		{name: "DOS device slashes", value: `//./C:/verified/subjects`},
		{name: "rooted native", value: `\subjects`},
		{name: "rooted slash", value: `/subjects`},
		{name: "drive relative", value: `C:verified\subjects`},
		{name: "current segment", value: `C:\verified\.\subjects`},
		{name: "parent segment", value: `C:\verified\..\subjects`},
		{name: "doubled native separator", value: `C:\verified\\subjects`},
		{name: "doubled slash", value: `C:/verified//subjects`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := normalizeReleaseArtifactDirectory(test.value); err == nil {
				t.Fatalf("Windows path mutation %q was accepted", test.value)
			}
		})
	}
}

func TestReleaseArtifactsPassesCanonicalWindowsRunnerPathToVerifier(t *testing.T) {
	t.Parallel()
	const input = `D:\a\_temp/go-distribution-release-verified`
	const want = `D:\a\_temp\go-distribution-release-verified`
	sentinel := errors.New("stop after observing normalized input")
	gate := verifier{verifySubjects: func(_ context.Context, actual string) (releaseSubjectSet, error) {
		if actual != want {
			t.Fatalf("verifier directory = %q, want %q", actual, want)
		}
		return nil, sentinel
	}}
	err := gate.releaseArtifacts(context.Background(), input)
	if err == nil {
		t.Fatal("releaseArtifacts() succeeded after verifier sentinel")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("releaseArtifacts() error = %v, want verifier sentinel", err)
	}
	if !strings.Contains(err.Error(), "verify release subjects") {
		t.Fatalf("releaseArtifacts() error = %v, want verifier context", err)
	}
}

func TestReleaseArtifactsCancellationPrecedesWindowsRunnerPathVerification(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	gate := verifier{verifySubjects: func(context.Context, string) (releaseSubjectSet, error) {
		called = true
		return nil, errors.New("unexpected verifier call")
	}}
	err := gate.releaseArtifacts(ctx, `D:\a\_temp/go-distribution-release-verified`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled releaseArtifacts() error = %v", err)
	}
	if called {
		t.Fatal("release verifier ran after cancellation")
	}
}

func TestWindowsReleaseExecutionRequiresEphemeralRunnerAcknowledgement(t *testing.T) {
	t.Parallel()
	if err := validateReleaseExecutionBoundary("1"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "0", "true", " 1"} {
		if err := validateReleaseExecutionBoundary(value); err == nil {
			t.Fatalf("Windows acknowledgement %q was accepted", value)
		}
	}
}
