package releaseverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

const maxVendorGraphBytes = 16 << 20

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

type listedModule struct {
	path    string
	version string
	main    bool
}

func verifySBOM(
	data []byte,
	modules []listedModule,
	version string,
	epoch time.Time,
) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var actual spdxDocument
	if err := decoder.Decode(&actual); err != nil {
		return fmt.Errorf("decode SPDX document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("SPDX document has trailing JSON content")
	}
	expected := expectedSBOM(modules, version, epoch)
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("SPDX document does not exactly match the trusted source module graph")
	}
	return nil
}

func sourceModules(source map[string][]byte) ([]listedModule, error) {
	goMod := source["go.mod"]
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return nil, fmt.Errorf("parse source go.mod: %w", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != modulePath {
		return nil, fmt.Errorf("source module is not %q", modulePath)
	}
	if len(parsed.Replace) != 0 {
		return nil, errors.New("source go.mod contains forbidden replace directives")
	}
	required := make(map[string]string, len(parsed.Require))
	for _, requirement := range parsed.Require {
		if _, duplicate := required[requirement.Mod.Path]; duplicate {
			return nil, fmt.Errorf("source go.mod repeats requirement %q", requirement.Mod.Path)
		}
		required[requirement.Mod.Path] = requirement.Mod.Version
	}
	vendor := source["vendor/modules.txt"]
	if len(vendor) > maxVendorGraphBytes {
		return nil, fmt.Errorf("source vendor/modules.txt exceeds %d bytes", maxVendorGraphBytes)
	}
	vendored := make(map[string]string, len(required))
	lines := strings.Split(string(vendor), "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			continue
		}
		module, found := parseVendoredModule(line[2:])
		if !found || index+1 >= len(lines) ||
			!strings.HasPrefix(lines[index+1], "## ") ||
			!slices.Contains(
				strings.FieldsFunc(lines[index+1][3:], func(value rune) bool {
					return value == ';' || value == ' '
				}),
				"explicit",
			) {
			return nil, fmt.Errorf("source vendor module header %q is invalid", line)
		}
		if _, duplicate := vendored[module.path]; duplicate {
			return nil, fmt.Errorf("source vendor module %q is duplicated", module.path)
		}
		vendored[module.path] = module.version
	}
	for name, version := range required {
		if vendored[name] != version {
			return nil, fmt.Errorf("source vendor graph does not contain exact %s@%s", name, version)
		}
	}
	for name, version := range vendored {
		if required[name] != version {
			return nil, fmt.Errorf("source vendor graph has undeclared %s@%s", name, version)
		}
	}
	modules := []listedModule{{path: modulePath, main: true}}
	for name, version := range vendored {
		modules = append(modules, listedModule{path: name, version: version})
	}
	slices.SortFunc(modules, func(left, right listedModule) int {
		return strings.Compare(left.path, right.path)
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
	return listedModule{path: fields[0], version: fields[1]}, true
}

func expectedSBOM(modules []listedModule, version string, epoch time.Time) spdxDocument {
	packages := make([]spdxPackage, 0, len(modules))
	rootID := ""
	for _, module := range modules {
		moduleVersion := module.version
		if module.main {
			moduleVersion = version
		}
		id := packageSPDXID(module.path, moduleVersion)
		if module.main {
			rootID = id
		}
		packages = append(packages, spdxPackage{
			Name:             module.path,
			SPDXID:           id,
			VersionInfo:      moduleVersion,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
			CopyrightText:    "NOASSERTION",
		})
	}
	relationships := make([]spdxRelationship, 0, len(packages)-1)
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
	var namespace strings.Builder
	namespace.WriteString(namespaceInput)
	for _, item := range packages {
		namespace.WriteByte('\n')
		namespace.WriteString(item.Name)
		namespace.WriteByte('@')
		namespace.WriteString(item.VersionInfo)
	}
	hash := sha256.Sum256([]byte(namespace.String()))
	return spdxDocument{
		SPDXVersion: "SPDX-2.3",
		DataLicense: "CC0-1.0",
		SPDXID:      "SPDXRef-DOCUMENT",
		Name:        "Spice " + version,
		DocumentNamespace: "https://github.com/spice-framework/toolchain/releases/" +
			version + "/spdx/" + hex.EncodeToString(hash[:]),
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
}

func packageSPDXID(name, version string) string {
	hash := sha256.Sum256([]byte(name + "@" + version))
	return "SPDXRef-Package-" + hex.EncodeToString(hash[:8])
}
