package goreleaseverify

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

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
	VersionInfo      string `json:"versionInfo"`
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

func expectedSBOM(policy releasePolicy, commit string, epoch time.Time, modules []selectedModule) spdxDocument {
	rootID := packageID(policy.module, policy.version)
	packages := []spdxPackage{spdxPackageValue(policy.module, policy.version)}
	relationships := []spdxRelationship{{
		SPDXElementID: "SPDXRef-DOCUMENT", RelationshipType: "DESCRIBES", RelatedSPDXElement: rootID,
	}}
	for _, item := range modules {
		packages = append(packages, spdxPackageValue(item.path, item.version))
		relationships = append(relationships, spdxRelationship{
			SPDXElementID: rootID, RelationshipType: "DEPENDS_ON",
			RelatedSPDXElement: packageID(item.path, item.version),
		})
	}
	namespaceSeed := policy.module + "@" + policy.version + "@" + commit
	namespaceDigest := sha256.Sum256([]byte(namespaceSeed))
	return spdxDocument{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT",
		Name: policy.repository + " " + policy.version,
		DocumentNamespace: strings.TrimSuffix(policy.source, "/") + "/releases/" +
			policy.version + "/spdx/v1/" + hex.EncodeToString(namespaceDigest[:]),
		CreationInfo: spdxCreationInfo{
			Created: epoch.UTC().Format(time.RFC3339),
			Creators: []string{
				"Organization: Spice Framework",
				"Tool: " + rendererIdentity,
			},
		},
		Packages: packages, Relationships: relationships,
	}
}

func spdxPackageValue(name, version string) spdxPackage {
	return spdxPackage{
		Name: name, SPDXID: packageID(name, version), VersionInfo: version,
		DownloadLocation: "NOASSERTION", FilesAnalyzed: false,
		LicenseConcluded: "NOASSERTION", LicenseDeclared: "NOASSERTION", CopyrightText: "NOASSERTION",
	}
}

func packageID(name, version string) string {
	digest := sha256.Sum256([]byte(name + "@" + version))
	return "SPDXRef-Package-" + hex.EncodeToString(digest[:8])
}
