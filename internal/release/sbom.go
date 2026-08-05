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
		DocumentNamespace: "https://github.com/spice-framework/spice/releases/" +
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
	modulePath := modfile.ModulePath(goMod)
	if modulePath == "" {
		return nil, fmt.Errorf("read release go.mod: module directive is missing")
	}
	modules := []listedModule{{Path: modulePath, Main: true}}
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
	for line := range strings.SplitSeq(string(vendorData), "\n") {
		if !strings.HasPrefix(line, "# ") ||
			strings.HasPrefix(line, "## ") {
			continue
		}
		module, found := parseVendoredModule(line[2:])
		if found {
			modules = append(modules, module)
		}
	}
	slices.SortFunc(modules, func(left, right listedModule) int {
		return strings.Compare(left.Path, right.Path)
	})
	return modules, nil
}

func parseVendoredModule(line string) (listedModule, bool) {
	left, replacement, replaced := strings.Cut(line, " => ")
	fields := strings.Fields(left)
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") {
		return listedModule{}, false
	}
	version := fields[1]
	if replaced {
		replacementFields := strings.Fields(replacement)
		if len(replacementFields) >= 2 &&
			strings.HasPrefix(replacementFields[1], "v") {
			version = replacementFields[1]
		}
	}
	return listedModule{Path: fields[0], Version: version}, true
}

func packageSPDXID(name, version string) string {
	sum := sha256.Sum256([]byte(name + "@" + version))
	return "SPDXRef-Package-" + hex.EncodeToString(sum[:8])
}
