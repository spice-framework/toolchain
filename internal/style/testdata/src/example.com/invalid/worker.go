package invalid

import "context"

var shared int // want "spice.style.package.mutable-global"

type Worker struct {
	ctx context.Context // want "spice.style.context.stored"
}

func NewWorker() *Worker {
	return &Worker{}
}

func helper() {} // want "spice.style.function.package-level"

func init() {} // want "spice.style.function.init"
