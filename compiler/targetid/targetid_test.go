package targetid

import "testing"

func TestDefault(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"":                   "application",
		"Commerce":           "commerce",
		"HTTPApplication":    "httpapplication",
		"9 invalid target!!": "invalid_target",
		"already__stable":    "already_stable",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got := Default(input); got != want {
				t.Fatalf("Default(%q) = %q, want %q", input, got, want)
			}
		})
	}
}
