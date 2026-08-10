package valid

import "embed"

// Assets owns embedded validation fixture bytes.
type Assets struct{}

//go:embed fixture.txt
var files embed.FS
