package style

import compilerstyle "github.com/spice-framework/toolchain/compiler/style"

// RuleLevel controls one shared style rule.
type RuleLevel = compilerstyle.RuleLevel

const (
	RuleLevelOff     = compilerstyle.RuleLevelOff
	RuleLevelWarning = compilerstyle.RuleLevelWarning
	RuleLevelError   = compilerstyle.RuleLevelError
)
