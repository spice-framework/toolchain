package invalid // want "spice.style.file.one-primary-type"

import "context"

func (worker *Worker) Run( // want "spice.style.file.method-owner"
	value string,
	ctx context.Context, // want "spice.style.context.first"
) (error, string) { // want "spice.style.error.last"
	return nil, value
}
