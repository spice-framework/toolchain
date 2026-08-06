package gitenv

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestReadOnlyRejectsAmbientRepositoryAndObjectSubstitution(t *testing.T) {
	t.Parallel()
	environment := []string{
		"PATH=preserved",
		"GIT_DIR=redirected",
		"git_work_tree=redirected",
		"GIT_OBJECT_DIRECTORY=objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=alternate",
		"GIT_REPLACE_REF_BASE=refs/evil",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_PARAMETERS='core.abbrev=4'",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=evil",
		"GIT_NO_REPLACE_OBJECTS=0",
	}
	got := ReadOnly(environment)
	if !slices.Contains(got, "PATH=preserved") ||
		!slices.Contains(got, "GIT_NO_REPLACE_OBJECTS=1") ||
		!slices.Contains(got, "GIT_OPTIONAL_LOCKS=0") ||
		!slices.Contains(got, "GIT_CONFIG_GLOBAL="+os.DevNull) {
		t.Fatalf("ReadOnly() = %v", got)
	}
	for _, entry := range got {
		if strings.Contains(strings.ToLower(entry), "redirected") ||
			strings.Contains(strings.ToLower(entry), "alternate") ||
			strings.Contains(strings.ToLower(entry), "evil") {
			t.Fatalf("ReadOnly() retained unsafe entry %q", entry)
		}
	}
}
