package style

import "errors"

// PackageVariableException is one exact language- or interoperability-required
// package variable boundary.
type PackageVariableException struct {
	Glob   string `json:"glob"`
	Symbol string `json:"symbol"`
	Type   string `json:"type"`
	Reason string `json:"reason"`
	Issue  string `json:"issue"`
}

func (exception PackageVariableException) validate() error {
	if err := validateGlob(exception.Glob); err != nil {
		return err
	}
	if exception.Symbol == "" {
		return errors.New("symbol is required")
	}
	if exception.Type == "" {
		return errors.New("type is required")
	}
	if exception.Reason == "" {
		return errors.New("reason is required")
	}
	if exception.Issue == "" {
		return errors.New("issue is required")
	}
	return nil
}
