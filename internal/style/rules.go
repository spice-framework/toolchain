package style

// Rules is the closed structural rule set for schema version one.
type Rules struct {
	OnePrimaryTypePerFile  RuleLevel `json:"onePrimaryTypePerFile"`
	MethodsInPrimaryFile   RuleLevel `json:"methodsInPrimaryTypeFile"`
	FileNameMatchesType    RuleLevel `json:"fileNameMatchesType"`
	PackageFunctions       RuleLevel `json:"packageFunctions"`
	ExplicitConstructors   RuleLevel `json:"explicitConstructors"`
	ExplicitManagedScopes  RuleLevel `json:"explicitManagedScopes"`
	BanInit                RuleLevel `json:"banInit"`
	BanMutablePackageState RuleLevel `json:"banMutablePackageState"`
	PrivateManagedFields   RuleLevel `json:"privateManagedFields"`
	ModuleOwnership        RuleLevel `json:"moduleOwnership"`
	RouteClassification    RuleLevel `json:"routeClassification"`
	ContextFirst           RuleLevel `json:"contextFirst"`
	ErrorLast              RuleLevel `json:"errorLast"`
	MaxTypeFileLines       int       `json:"maxTypeFileLines"`
}

func (rules Rules) validate() error {
	levels := []struct {
		name  string
		value RuleLevel
	}{
		{"onePrimaryTypePerFile", rules.OnePrimaryTypePerFile},
		{"methodsInPrimaryTypeFile", rules.MethodsInPrimaryFile},
		{"fileNameMatchesType", rules.FileNameMatchesType},
		{"packageFunctions", rules.PackageFunctions},
		{"explicitConstructors", rules.ExplicitConstructors},
		{"explicitManagedScopes", rules.ExplicitManagedScopes},
		{"banInit", rules.BanInit},
		{"banMutablePackageState", rules.BanMutablePackageState},
		{"privateManagedFields", rules.PrivateManagedFields},
		{"moduleOwnership", rules.ModuleOwnership},
		{"routeClassification", rules.RouteClassification},
		{"contextFirst", rules.ContextFirst},
		{"errorLast", rules.ErrorLast},
	}
	for _, entry := range levels {
		if !entry.value.valid() {
			return newConfigurationError("rule " + entry.name + " has unsupported level")
		}
	}
	if rules.MaxTypeFileLines < 1 || rules.MaxTypeFileLines > 10_000 {
		return newConfigurationError("maxTypeFileLines must be between 1 and 10000")
	}
	return nil
}
