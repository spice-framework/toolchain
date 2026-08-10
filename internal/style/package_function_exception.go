package style

// PackageFunctionException is one exact tool- or language-required package
// function boundary.
type PackageFunctionException struct {
	Glob             string `json:"glob"`
	Symbol           string `json:"symbol,omitempty"`
	SymbolPattern    string `json:"symbolPattern,omitempty"`
	ContributionKind string `json:"contributionKind,omitempty"`
	Maximum          int    `json:"maximum,omitempty"`
	Reason           string `json:"reason"`
}
