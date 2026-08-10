package style

// RuleLevel controls one structural style rule.
type RuleLevel string

const (
	RuleLevelOff     RuleLevel = "off"
	RuleLevelWarning RuleLevel = "warning"
	RuleLevelError   RuleLevel = "error"
)

func (level RuleLevel) valid() bool {
	switch level {
	case RuleLevelOff, RuleLevelWarning, RuleLevelError:
		return true
	default:
		return false
	}
}
