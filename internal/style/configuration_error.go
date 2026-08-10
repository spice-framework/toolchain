package style

// ConfigurationError is a fixed, secret-safe style configuration failure.
type ConfigurationError struct {
	problem string
}

func newConfigurationError(problem string) ConfigurationError {
	return ConfigurationError{problem: problem}
}

func (failure ConfigurationError) Error() string {
	return "invalid Spice style configuration: " + failure.problem
}
