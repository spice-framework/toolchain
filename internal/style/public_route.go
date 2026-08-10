package style

// PublicRoute is one reviewed public-route exception used by the Spice-aware
// validation phase.
type PublicRoute struct {
	Package  string `json:"package"`
	Receiver string `json:"receiver"`
	Method   string `json:"method"`
	Reason   string `json:"reason"`
	Issue    string `json:"issue"`
}
