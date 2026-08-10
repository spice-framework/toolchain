package valid

import "context"

type HTTPController struct{}

func NewHTTPController() *HTTPController {
	return &HTTPController{}
}

func (controller *HTTPController) Run(context.Context) error {
	return nil
}
