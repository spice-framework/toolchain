package goreleaseverify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

func validateCommittedModule(
	ctx context.Context,
	source sourceIdentity,
	policy releasePolicy,
) ([]selectedModule, error) {
	for _, name := range []string{
		"LICENSE", "README.md", "go.mod", "go.sum", policy.metadataFile, "vendor/modules.txt",
	} {
		if _, err := readGitBlob(ctx, source, name, maxModuleGraph); err != nil {
			return nil, err
		}
	}
	intentData, err := readGitBlob(ctx, source, policy.metadataFile, maxControlFile)
	if err != nil {
		return nil, err
	}
	var intent releaseIntent
	if decodeErr := decodeStrictJSON(intentData, &intent); decodeErr != nil {
		return nil, fmt.Errorf("decode committed Go release intent: %w", decodeErr)
	}
	wantIntent := releaseIntent{
		Schema: metadataSchema, Profile: ProfileGoModule, Repository: policy.repository,
		Module: policy.module, Version: policy.version,
	}
	if intent != wantIntent {
		return nil, errors.New("committed Go release intent does not match independent policy")
	}
	goMod, err := readGitBlob(ctx, source, "go.mod", maxModuleGraph)
	if err != nil {
		return nil, err
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return nil, fmt.Errorf("parse committed go.mod: %w", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != policy.module {
		return nil, fmt.Errorf("committed Go module does not match policy module %q", policy.module)
	}
	if parsed.Go == nil || parsed.Go.Version != "1.26.0" ||
		parsed.Toolchain == nil || parsed.Toolchain.Name != "go1.26.5" {
		return nil, errors.New("committed Go module must declare go 1.26.0 and toolchain go1.26.5")
	}
	if len(parsed.Replace) != 0 {
		return nil, errors.New("committed Go module must not contain replace directives")
	}
	direct := make(map[string]string, len(parsed.Require))
	selected := make([]selectedModule, 0, len(parsed.Require))
	for _, requirement := range parsed.Require {
		if _, duplicate := direct[requirement.Mod.Path]; duplicate {
			return nil, fmt.Errorf("committed Go module repeats requirement %s", requirement.Mod.Path)
		}
		if checkErr := module.Check(requirement.Mod.Path, requirement.Mod.Version); checkErr != nil ||
			!canonicalVersion(requirement.Mod.Version) {
			return nil, fmt.Errorf("committed Go module requirement %s@%s is not canonical", requirement.Mod.Path, requirement.Mod.Version)
		}
		direct[requirement.Mod.Path] = requirement.Mod.Version
		selected = append(selected, selectedModule{path: requirement.Mod.Path, version: requirement.Mod.Version})
	}
	for _, required := range policy.requiredModules {
		version, found := direct[required.path]
		if !found {
			return nil, fmt.Errorf("committed Go module does not require policy module %s", required.path)
		}
		if version != required.version {
			return nil, fmt.Errorf(
				"committed Go module requires %s@%s; independent policy requires %s",
				required.path,
				version,
				required.version,
			)
		}
	}
	vendorData, err := readGitBlob(ctx, source, "vendor/modules.txt", maxModuleGraph)
	if err != nil {
		return nil, err
	}
	vendored, err := parseVendorModules(vendorData)
	if err != nil {
		return nil, err
	}
	if err := compareModuleGraph(selected, vendored); err != nil {
		return nil, err
	}
	slices.SortFunc(vendored, func(left, right selectedModule) int {
		return strings.Compare(left.path, right.path)
	})
	return vendored, nil
}

func canonicalVersion(version string) bool {
	if !semver.IsValid(version) {
		return false
	}
	withoutBuild, _, _ := strings.Cut(version, "+")
	return semver.Canonical(version) == withoutBuild
}

func parseVendorModules(content []byte) ([]selectedModule, error) {
	if len(content) == 0 || len(content) > maxModuleGraph {
		return nil, errors.New("committed vendor/modules.txt is empty or oversized")
	}
	lines := strings.Split(string(content), "\n")
	result := make([]selectedModule, 0)
	seen := make(map[string]struct{})
	for index, line := range lines {
		if !strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "# "))
		if len(fields) != 2 || !canonicalVersion(fields[1]) {
			return nil, fmt.Errorf("committed vendor module header %q is malformed or uses a replacement", line)
		}
		if index+1 >= len(lines) || !vendorExplicit(lines[index+1]) {
			return nil, fmt.Errorf("committed vendor module %s is not marked explicit", fields[0])
		}
		if _, duplicate := seen[fields[0]]; duplicate {
			return nil, fmt.Errorf("committed vendor graph repeats module %s", fields[0])
		}
		seen[fields[0]] = struct{}{}
		result = append(result, selectedModule{path: fields[0], version: fields[1]})
	}
	return result, nil
}

func vendorExplicit(line string) bool {
	if !strings.HasPrefix(line, "## ") {
		return false
	}
	return slices.Contains(
		strings.FieldsFunc(strings.TrimPrefix(line, "## "), func(character rune) bool {
			return character == ';' || character == ' '
		}),
		"explicit",
	)
}

func compareModuleGraph(expected, actual []selectedModule) error {
	want := make(map[string]string, len(expected))
	for _, item := range expected {
		want[item.path] = item.version
	}
	for _, item := range actual {
		version, found := want[item.path]
		if !found || version != item.version {
			return fmt.Errorf("committed vendor module %s@%s is not selected by go.mod", item.path, item.version)
		}
		delete(want, item.path)
	}
	if len(want) != 0 {
		missing := make([]string, 0, len(want))
		for path := range want {
			missing = append(missing, path)
		}
		slices.Sort(missing)
		return fmt.Errorf("committed vendor graph is missing selected modules %v", missing)
	}
	return nil
}
