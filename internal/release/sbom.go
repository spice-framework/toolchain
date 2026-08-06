package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

const maxVendorGraphBytes = 16 << 20

const releaseModulePath = "github.com/spice-framework/toolchain"

type listedModule struct {
	Path    string
	Version string
	Main    bool
}

type spdxDocument struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      spdxCreationInfo   `json:"creationInfo"`
	Packages          []spdxPackage      `json:"packages"`
	Relationships     []spdxRelationship `json:"relationships"`
}

type spdxCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	Name             string `json:"name"`
	SPDXID           string `json:"SPDXID"`
	VersionInfo      string `json:"versionInfo,omitempty"`
	DownloadLocation string `json:"downloadLocation"`
	FilesAnalyzed    bool   `json:"filesAnalyzed"`
	LicenseConcluded string `json:"licenseConcluded"`
	LicenseDeclared  string `json:"licenseDeclared"`
	CopyrightText    string `json:"copyrightText"`
}

type spdxRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
}

func buildSBOM(
	ctx context.Context,
	root string,
	version string,
	epoch time.Time,
) ([]byte, error) {
	modules, err := listModules(ctx, root)
	if err != nil {
		return nil, err
	}
	packages := make([]spdxPackage, 0, len(modules))
	relationships := make([]spdxRelationship, 0, len(modules))
	rootID := ""
	for _, module := range modules {
		moduleVersion := module.Version
		if module.Main {
			moduleVersion = version
		}
		id := packageSPDXID(module.Path, moduleVersion)
		if module.Main {
			rootID = id
		}
		packages = append(packages, spdxPackage{
			Name:             module.Path,
			SPDXID:           id,
			VersionInfo:      moduleVersion,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
			CopyrightText:    "NOASSERTION",
		})
	}
	if rootID == "" {
		return nil, fmt.Errorf("build release SBOM: module graph has no main module")
	}
	for _, item := range packages {
		if item.SPDXID == rootID {
			continue
		}
		relationships = append(relationships, spdxRelationship{
			SPDXElementID:      rootID,
			RelationshipType:   "DEPENDS_ON",
			RelatedSPDXElement: item.SPDXID,
		})
	}
	namespaceInput := version + "\n" + epoch.UTC().Format(time.RFC3339)
	var namespaceBuilder strings.Builder
	namespaceBuilder.WriteString(namespaceInput)
	for _, item := range packages {
		namespaceBuilder.WriteByte('\n')
		namespaceBuilder.WriteString(item.Name)
		namespaceBuilder.WriteByte('@')
		namespaceBuilder.WriteString(item.VersionInfo)
	}
	namespaceHash := sha256.Sum256([]byte(namespaceBuilder.String()))
	document := spdxDocument{
		SPDXVersion: "SPDX-2.3",
		DataLicense: "CC0-1.0",
		SPDXID:      "SPDXRef-DOCUMENT",
		Name:        "Spice " + version,
		DocumentNamespace: "https://github.com/spice-framework/toolchain/releases/" +
			version + "/spdx/" + hex.EncodeToString(namespaceHash[:]),
		CreationInfo: spdxCreationInfo{
			Created: epoch.UTC().Format(time.RFC3339),
			Creators: []string{
				"Organization: Spice Authors",
				"Tool: github.com/spice-framework/toolchain/cmd/spice-release",
			},
		},
		Packages:      packages,
		Relationships: relationships,
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release SBOM: %w", err)
	}
	return append(data, '\n'), nil
}

func listModules(ctx context.Context, root string) ([]listedModule, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read release module graph: %w", err)
	}
	goMod, err := readScopedFile(root, "go.mod")
	if err != nil {
		return nil, fmt.Errorf("read release go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return nil, fmt.Errorf("read release go.mod: %w", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path == "" {
		return nil, fmt.Errorf("read release go.mod: module directive is missing")
	}
	if parsed.Module.Mod.Path != releaseModulePath {
		return nil, fmt.Errorf(
			"read release go.mod: module is %q, require %q",
			parsed.Module.Mod.Path,
			releaseModulePath,
		)
	}
	if len(parsed.Replace) != 0 {
		return nil, fmt.Errorf("read release go.mod: replace directives are forbidden")
	}
	required := make(map[string]string, len(parsed.Require))
	for _, requirement := range parsed.Require {
		required[requirement.Mod.Path] = requirement.Mod.Version
	}
	vendorData, err := readScopedFile(root, "vendor/modules.txt")
	if err != nil {
		return nil, fmt.Errorf("read release vendor module graph: %w", err)
	}
	if len(vendorData) > maxVendorGraphBytes {
		return nil, fmt.Errorf(
			"read release vendor module graph: file exceeds %d bytes",
			maxVendorGraphBytes,
		)
	}
	vendored := make(map[string]string, len(required))
	lines := strings.Split(string(vendorData), "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, "# ") ||
			strings.HasPrefix(line, "## ") {
			continue
		}
		module, found := parseVendoredModule(line[2:])
		if !found {
			return nil, fmt.Errorf(
				"read release vendor module graph: invalid module header %q",
				line,
			)
		}
		if index+1 >= len(lines) ||
			!strings.HasPrefix(lines[index+1], "## ") ||
			!slices.Contains(
				strings.FieldsFunc(lines[index+1][3:], func(value rune) bool {
					return value == ';' || value == ' '
				}),
				"explicit",
			) {
			return nil, fmt.Errorf(
				"read release vendor module graph: module %q is not explicit",
				module.Path,
			)
		}
		if _, duplicate := vendored[module.Path]; duplicate {
			return nil, fmt.Errorf(
				"read release vendor module graph: duplicate module %q",
				module.Path,
			)
		}
		vendored[module.Path] = module.Version
	}
	for path, version := range required {
		vendorVersion, found := vendored[path]
		if !found {
			return nil, fmt.Errorf(
				"read release vendor module graph: required module %s@%s is missing",
				path,
				version,
			)
		}
		if vendorVersion != version {
			return nil, fmt.Errorf(
				"read release vendor module graph: module %s is %s, require %s",
				path,
				vendorVersion,
				version,
			)
		}
	}
	for path, version := range vendored {
		if _, found := required[path]; !found {
			return nil, fmt.Errorf(
				"read release vendor module graph: undeclared module %s@%s",
				path,
				version,
			)
		}
	}
	modules := make([]listedModule, 0, len(vendored)+1)
	modules = append(modules, listedModule{Path: releaseModulePath, Main: true})
	for path, version := range vendored {
		modules = append(modules, listedModule{Path: path, Version: version})
	}
	slices.SortFunc(modules, func(left, right listedModule) int {
		return strings.Compare(left.Path, right.Path)
	})
	return modules, nil
}

func parseVendoredModule(line string) (listedModule, bool) {
	if strings.Contains(line, " => ") {
		return listedModule{}, false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") {
		return listedModule{}, false
	}
	return listedModule{Path: fields[0], Version: fields[1]}, true
}

func packageSPDXID(name, version string) string {
	sum := sha256.Sum256([]byte(name + "@" + version))
	return "SPDXRef-Package-" + hex.EncodeToString(sum[:8])
}
