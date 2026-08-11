package style

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	maximumConfigurationBytes = 1 << 20
	styleSchemaVersion        = 2
	// CanonicalPolicyCommit is the exact Spice commit owning schema two.
	CanonicalPolicyCommit = "0e79bc4f3b294cd0a429598c4921391f2e4d10e2"
	// CanonicalPolicySHA256 is the reviewed canonical CODE_STYLE.md identity.
	CanonicalPolicySHA256 = "09c014e2d7eb93bf2b395e24e4e6ff2466c05d164d4778a11cf7433164bffb76"
)

// Configuration is the immutable schema-two java-structured style contract.
type Configuration struct {
	SchemaVersion             int                        `json:"schemaVersion"`
	Profile                   string                     `json:"profile"`
	SourceRoots               []string                   `json:"sourceRoots"`
	GeneratedRoots            []string                   `json:"generatedRoots"`
	BuildSelections           []BuildSelection           `json:"buildSelections"`
	Rules                     Rules                      `json:"rules"`
	PublicRoutes              []PublicRoute              `json:"publicRoutes"`
	AllowedBoundaryFiles      []string                   `json:"allowedBoundaryFiles"`
	PackageFunctionExceptions []PackageFunctionException `json:"packageFunctionExceptions"`
	PackageVariableExceptions []PackageVariableException `json:"packageVariableExceptions"`
}

// BuildSelection is one exact, explicitly named Go build context.
type BuildSelection struct {
	Name        string   `json:"name"`
	SourceRoots []string `json:"sourceRoots"`
	GOOS        string   `json:"goos"`
	GOARCH      string   `json:"goarch"`
	CGOEnabled  *bool    `json:"cgoEnabled"`
	Tags        []string `json:"tags"`
}

// PublicRoute is one reviewed public-route exception for typed validation.
type PublicRoute struct {
	Package  string `json:"package"`
	Receiver string `json:"receiver"`
	Method   string `json:"method"`
	Reason   string `json:"reason"`
	Issue    string `json:"issue"`
}

// PackageFunctionException is one exact required package-function boundary.
type PackageFunctionException struct {
	Glob             string `json:"glob"`
	Symbol           string `json:"symbol,omitempty"`
	SymbolPattern    string `json:"symbolPattern,omitempty"`
	ContributionKind string `json:"contributionKind,omitempty"`
	Maximum          int    `json:"maximum,omitempty"`
	Reason           string `json:"reason"`
}

// PackageVariableException is one exact required package-variable boundary.
type PackageVariableException struct {
	Glob   string `json:"glob"`
	Symbol string `json:"symbol"`
	Type   string `json:"type"`
	Reason string `json:"reason"`
	Issue  string `json:"issue"`
}

// RuleLevel controls one style rule.
type RuleLevel string

const (
	RuleLevelOff     RuleLevel = "off"
	RuleLevelWarning RuleLevel = "warning"
	RuleLevelError   RuleLevel = "error"
)

// Rules is the closed schema-two rule set.
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

// ConfigurationError is one stable, secret-safe style configuration failure.
type ConfigurationError struct {
	code    string
	problem string
}

func (failure ConfigurationError) Error() string {
	return failure.code + ": " + failure.problem
}

// Code returns the stable canonical configuration diagnostic code.
func (failure ConfigurationError) Code() string {
	return failure.code
}

// LoadConfiguration reads and validates one bounded schema-two configuration.
func LoadConfiguration(filePath string) (Configuration, error) {
	file, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		return Configuration{}, fmt.Errorf("open Spice style configuration: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maximumConfigurationBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return Configuration{}, fmt.Errorf(
			"read Spice style configuration: %w",
			errors.Join(readErr, closeErr),
		)
	}
	if closeErr != nil {
		return Configuration{}, fmt.Errorf("close Spice style configuration: %w", closeErr)
	}
	if len(content) > maximumConfigurationBytes {
		return Configuration{}, configurationSchemaError("file exceeds 1 MiB")
	}
	return DecodeConfiguration(content)
}

// DecodeConfiguration strictly decodes and validates one configuration value.
func DecodeConfiguration(content []byte) (Configuration, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var configuration Configuration
	if err := decoder.Decode(&configuration); err != nil {
		return Configuration{}, configurationSchemaError("decode JSON: " + err.Error())
	}
	if err := requireConfigurationJSONEnd(decoder); err != nil {
		return Configuration{}, err
	}
	if err := configuration.Validate(); err != nil {
		return Configuration{}, err
	}
	return configuration.Clone(), nil
}

func requireConfigurationJSONEnd(decoder *json.Decoder) error {
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return configurationSchemaError("decode trailing JSON data: " + err.Error())
	}
	return configurationSchemaError("configuration contains more than one JSON value")
}

// Validate checks the closed schema, exact selections, and rule capabilities.
func (configuration Configuration) Validate() error {
	if configuration.SchemaVersion == 1 {
		return configurationSchemaError(
			"schemaVersion 1 is retired; migrate to schemaVersion 2 and declare exact buildSelections",
		)
	}
	if configuration.SchemaVersion != styleSchemaVersion {
		return configurationSchemaError(fmt.Sprintf(
			"schemaVersion is %d, want %d",
			configuration.SchemaVersion,
			styleSchemaVersion,
		))
	}
	if configuration.Profile != string(ProfileJavaStructured) {
		return configurationSchemaError("profile must equal java-structured")
	}
	if err := validateConfigurationRoots("sourceRoots", configuration.SourceRoots); err != nil {
		return err
	}
	if err := validateConfigurationRoots("generatedRoots", configuration.GeneratedRoots); err != nil {
		return err
	}
	if err := validateGeneratedRoots(configuration); err != nil {
		return err
	}
	if err := configuration.Rules.validate(); err != nil {
		return err
	}
	if err := validateRuleCapabilities(configuration.Rules); err != nil {
		return err
	}
	if err := validateBuildSelections(configuration); err != nil {
		return err
	}
	return configuration.validateExceptions()
}

func validateConfigurationRoots(field string, roots []string) error {
	if len(roots) == 0 {
		return configurationSourceError(field + " must not be empty")
	}
	if !slices.IsSorted(roots) {
		return configurationSourceError(field + " must be sorted")
	}
	previous := ""
	seen := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == previous {
			return configurationSourceError(field + " contains duplicate " + strconv.Quote(root))
		}
		if !validConfigurationRoot(root) {
			return configurationSourceError(field + " contains invalid root " + strconv.Quote(root))
		}
		for _, owner := range seen {
			if strings.HasPrefix(root, owner+"/") {
				return configurationSourceError(
					field + " contains overlapping roots " + strconv.Quote(owner) + " and " + strconv.Quote(root),
				)
			}
		}
		seen = append(seen, root)
		previous = root
	}
	return nil
}

func validConfigurationRoot(root string) bool {
	return root != "" && root != "." && path.Clean(root) == root && !path.IsAbs(root) &&
		!strings.HasPrefix(root, "../") && !strings.Contains(root, "\\")
}

func validateGeneratedRoots(configuration Configuration) error {
	for _, generated := range configuration.GeneratedRoots {
		owner := ""
		for _, source := range configuration.SourceRoots {
			if generated == source {
				return configurationSourceError(
					"generated root " + strconv.Quote(generated) + " must not be reintroduced as a source root",
				)
			}
			if strings.HasPrefix(generated, source+"/") {
				owner = source
			}
		}
		if owner == "" {
			return configurationSourceError(
				"generated root " + strconv.Quote(generated) + " is outside the declared source universe",
			)
		}
	}
	return nil
}

func (rules Rules) validate() error {
	for _, entry := range rules.entries() {
		if !entry.level.valid() {
			return configurationSchemaError("rule " + entry.name + " has unsupported level")
		}
	}
	if rules.MaxTypeFileLines < 1 || rules.MaxTypeFileLines > 10_000 {
		return configurationSchemaError("maxTypeFileLines must be between 1 and 10000")
	}
	return nil
}

type ruleEntry struct {
	name  string
	level RuleLevel
}

func (rules Rules) entries() []ruleEntry {
	return []ruleEntry{
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
}

func (level RuleLevel) valid() bool {
	switch level {
	case RuleLevelOff, RuleLevelWarning, RuleLevelError:
		return true
	default:
		return false
	}
}

type ruleCapability struct {
	name          string
	requiredPhase string
	implemented   bool
}

func validateRuleCapabilities(rules Rules) error {
	levels := make(map[string]RuleLevel, len(rules.entries()))
	for _, entry := range rules.entries() {
		levels[entry.name] = entry.level
	}
	for _, capability := range []ruleCapability{
		{name: "onePrimaryTypePerFile", requiredPhase: "structural", implemented: true},
		{name: "methodsInPrimaryTypeFile", requiredPhase: "structural", implemented: true},
		{name: "fileNameMatchesType", requiredPhase: "structural", implemented: true},
		{name: "packageFunctions", requiredPhase: "structural + typed", implemented: true},
		{name: "explicitConstructors", requiredPhase: "typed", implemented: true},
		{name: "explicitManagedScopes", requiredPhase: "typed", implemented: true},
		{name: "banInit", requiredPhase: "structural", implemented: true},
		{name: "banMutablePackageState", requiredPhase: "structural", implemented: true},
		{name: "privateManagedFields", requiredPhase: "typed", implemented: true},
		{name: "moduleOwnership", requiredPhase: "typed", implemented: true},
		{name: "routeClassification", requiredPhase: "typed", implemented: true},
		{name: "contextFirst", requiredPhase: "structural", implemented: true},
		{name: "errorLast", requiredPhase: "structural", implemented: true},
		{name: "maxTypeFileLines", requiredPhase: "structural", implemented: true},
	} {
		enabled := capability.name == "maxTypeFileLines" || levels[capability.name] != RuleLevelOff
		if enabled && !capability.implemented {
			return configurationCapabilityError(fmt.Sprintf(
				"enabled rule %q requires an unimplemented %s phase",
				capability.name,
				capability.requiredPhase,
			))
		}
	}
	return nil
}

type buildSelectionState struct {
	declaredRoots map[string]struct{}
	coveredRoots  map[string]struct{}
	names         map[string]struct{}
	identities    map[string]struct{}
	previousKey   string
}

func validateBuildSelections(configuration Configuration) error {
	if len(configuration.BuildSelections) == 0 {
		return configurationBuildError("buildSelections must not be empty")
	}
	state := newBuildSelectionState(configuration)
	for index, selection := range configuration.BuildSelections {
		if err := state.validate(index, selection); err != nil {
			return err
		}
	}
	for _, root := range configuration.SourceRoots {
		if _, covered := state.coveredRoots[root]; !covered {
			return configurationSourceError("source root " + strconv.Quote(root) + " is not covered by a build selection")
		}
	}
	return nil
}

func newBuildSelectionState(configuration Configuration) *buildSelectionState {
	declaredRoots := make(map[string]struct{}, len(configuration.SourceRoots))
	for _, root := range configuration.SourceRoots {
		declaredRoots[root] = struct{}{}
	}
	return &buildSelectionState{
		declaredRoots: declaredRoots,
		coveredRoots:  make(map[string]struct{}, len(configuration.SourceRoots)),
		names:         make(map[string]struct{}, len(configuration.BuildSelections)),
		identities:    make(map[string]struct{}, len(configuration.BuildSelections)),
	}
}

func (state *buildSelectionState) validate(index int, selection BuildSelection) error {
	matched, err := regexp.MatchString(`^[a-z0-9]+(?:-[a-z0-9]+)*$`, selection.Name)
	if err != nil || !matched {
		return configurationBuildError(fmt.Sprintf("selection %d has invalid name %q", index, selection.Name))
	}
	if _, found := state.names[selection.Name]; found {
		return configurationBuildError("duplicate selection name " + strconv.Quote(selection.Name))
	}
	state.names[selection.Name] = struct{}{}
	if selection.CGOEnabled == nil {
		return configurationBuildError("selection " + strconv.Quote(selection.Name) + " omits cgoEnabled")
	}
	if !supportedBuildPair(selection.GOOS, selection.GOARCH) {
		return configurationBuildError(fmt.Sprintf(
			"selection %q uses unsupported pair %s/%s",
			selection.Name,
			selection.GOOS,
			selection.GOARCH,
		))
	}
	if err := validateConfigurationRoots("selection "+selection.Name+" sourceRoots", selection.SourceRoots); err != nil {
		return err
	}
	for _, root := range selection.SourceRoots {
		if _, found := state.declaredRoots[root]; !found {
			return configurationSourceError(fmt.Sprintf(
				"selection %q uses undeclared root %q",
				selection.Name,
				root,
			))
		}
		state.coveredRoots[root] = struct{}{}
	}
	if err := validateBuildTags(selection); err != nil {
		return err
	}
	identity := buildSelectionIdentity(selection)
	if _, found := state.identities[identity]; found {
		return configurationBuildError("selection " + strconv.Quote(selection.Name) + " duplicates a build context")
	}
	state.identities[identity] = struct{}{}
	key := buildSelectionKey(selection)
	if state.previousKey != "" && key < state.previousKey {
		return configurationBuildError("selection " + strconv.Quote(selection.Name) + " is out of canonical order")
	}
	state.previousKey = key
	return nil
}

func validateBuildTags(selection BuildSelection) error {
	if !slices.IsSorted(selection.Tags) {
		return configurationBuildError("selection " + strconv.Quote(selection.Name) + " tags must be sorted")
	}
	pattern := regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.]*$`)
	previous := ""
	for _, tag := range selection.Tags {
		if !pattern.MatchString(tag) || tag == "cgo" {
			return configurationBuildError(fmt.Sprintf("selection %q has invalid tag %q", selection.Name, tag))
		}
		if tag == previous {
			return configurationBuildError(fmt.Sprintf("selection %q has duplicate tag %q", selection.Name, tag))
		}
		previous = tag
	}
	return nil
}

func buildSelectionIdentity(selection BuildSelection) string {
	return strings.Join([]string{
		selection.GOOS,
		selection.GOARCH,
		strconv.FormatBool(*selection.CGOEnabled),
		strings.Join(selection.Tags, ","),
	}, "\x00")
}

func buildSelectionKey(selection BuildSelection) string {
	return buildSelectionIdentity(selection) + "\x00" +
		strings.Join(selection.SourceRoots, ",") + "\x00" + selection.Name
}

func supportedBuildPair(goos, goarch string) bool {
	return slices.Contains(supportedBuildPairs(), goos+"/"+goarch)
}

func supportedBuildPairs() []string {
	return strings.Fields(`
		aix/ppc64 android/386 android/amd64 android/arm android/arm64
		darwin/amd64 darwin/arm64 dragonfly/amd64 freebsd/386 freebsd/amd64
		freebsd/arm freebsd/arm64 illumos/amd64 ios/amd64 ios/arm64 js/wasm
		linux/386 linux/amd64 linux/arm linux/arm64 linux/loong64 linux/mips
		linux/mips64 linux/mips64le linux/mipsle linux/ppc64 linux/ppc64le
		linux/riscv64 linux/s390x netbsd/386 netbsd/amd64 netbsd/arm
		netbsd/arm64 openbsd/386 openbsd/amd64 openbsd/arm openbsd/arm64
		openbsd/ppc64 openbsd/riscv64 plan9/386 plan9/amd64 plan9/arm
		solaris/amd64 wasip1/wasm windows/386 windows/amd64 windows/arm64
	`)
}

func (configuration Configuration) validateExceptions() error {
	for index, route := range configuration.PublicRoutes {
		if strings.TrimSpace(route.Package) == "" || strings.TrimSpace(route.Receiver) == "" ||
			strings.TrimSpace(route.Method) == "" || strings.TrimSpace(route.Reason) == "" ||
			strings.TrimSpace(route.Issue) == "" {
			return configurationSchemaError(fmt.Sprintf("publicRoutes[%d] is incomplete", index))
		}
	}
	for index, pattern := range configuration.AllowedBoundaryFiles {
		if err := validateConfigurationGlob(pattern); err != nil {
			return configurationSchemaError(fmt.Sprintf("allowedBoundaryFiles[%d]: %s", index, err))
		}
	}
	for index, exception := range configuration.PackageFunctionExceptions {
		if err := exception.validate(); err != nil {
			return configurationSchemaError(fmt.Sprintf("packageFunctionExceptions[%d]: %s", index, err))
		}
	}
	for index, exception := range configuration.PackageVariableExceptions {
		if err := exception.validate(); err != nil {
			return configurationSchemaError(fmt.Sprintf("packageVariableExceptions[%d]: %s", index, err))
		}
	}
	return nil
}

func validateConfigurationGlob(pattern string) error {
	if pattern == "" || path.IsAbs(pattern) || strings.Contains(pattern, "\\") {
		return errors.New("glob must be a non-empty slash-separated relative pattern")
	}
	if strings.Contains(pattern, "..") {
		return errors.New("glob must not contain parent traversal")
	}
	return nil
}

func (exception PackageFunctionException) validate() error {
	if err := validateConfigurationGlob(exception.Glob); err != nil {
		return err
	}
	if strings.TrimSpace(exception.Reason) == "" {
		return errors.New("reason is required")
	}
	selectors := 0
	if exception.Symbol != "" {
		selectors++
	}
	if exception.SymbolPattern != "" {
		selectors++
		if _, err := regexp.Compile(exception.SymbolPattern); err != nil {
			return errors.New("symbolPattern is invalid")
		}
	}
	if exception.ContributionKind != "" {
		selectors++
		switch exception.ContributionKind {
		case "application", "event-topic", "provider":
		default:
			return errors.New("contributionKind must be application, event-topic, or provider")
		}
	}
	if selectors != 1 {
		return errors.New("exactly one symbol, symbolPattern, or contributionKind is required")
	}
	if exception.Maximum < 0 {
		return errors.New("maximum must not be negative")
	}
	if exception.ContributionKind != "" && exception.Maximum == 0 {
		return errors.New("contributionKind requires a positive maximum")
	}
	if exception.ContributionKind == "" && exception.Maximum != 0 {
		return errors.New("maximum is valid only with contributionKind")
	}
	return nil
}

func (exception PackageVariableException) validate() error {
	if err := validateConfigurationGlob(exception.Glob); err != nil {
		return err
	}
	if exception.Symbol == "" || exception.Type == "" || strings.TrimSpace(exception.Reason) == "" ||
		strings.TrimSpace(exception.Issue) == "" {
		return errors.New("symbol, type, reason, and issue are required")
	}
	return nil
}

// Clone returns a defensive copy safe for a separate analysis phase.
func (configuration Configuration) Clone() Configuration {
	configuration.SourceRoots = slices.Clone(configuration.SourceRoots)
	configuration.GeneratedRoots = slices.Clone(configuration.GeneratedRoots)
	configuration.BuildSelections = slices.Clone(configuration.BuildSelections)
	for index := range configuration.BuildSelections {
		configuration.BuildSelections[index].SourceRoots = slices.Clone(configuration.BuildSelections[index].SourceRoots)
		configuration.BuildSelections[index].Tags = slices.Clone(configuration.BuildSelections[index].Tags)
		if configuration.BuildSelections[index].CGOEnabled != nil {
			value := *configuration.BuildSelections[index].CGOEnabled
			configuration.BuildSelections[index].CGOEnabled = &value
		}
	}
	configuration.PublicRoutes = slices.Clone(configuration.PublicRoutes)
	configuration.AllowedBoundaryFiles = slices.Clone(configuration.AllowedBoundaryFiles)
	configuration.PackageFunctionExceptions = slices.Clone(configuration.PackageFunctionExceptions)
	configuration.PackageVariableExceptions = slices.Clone(configuration.PackageVariableExceptions)
	return configuration
}

func configurationSchemaError(problem string) ConfigurationError {
	return ConfigurationError{code: "spice.style.configuration.schema", problem: problem}
}

func configurationCapabilityError(problem string) ConfigurationError {
	return ConfigurationError{code: "spice.style.configuration.unsupported-rule", problem: problem}
}

func configurationSourceError(problem string) ConfigurationError {
	return ConfigurationError{code: "spice.style.configuration.source-selection", problem: problem}
}

func configurationBuildError(problem string) ConfigurationError {
	return ConfigurationError{code: "spice.style.configuration.build-selection", problem: problem}
}
