package scaffold

import compilerstyle "github.com/spice-framework/toolchain/compiler/style"

type styleScaffold struct{}

func (styleScaffold) readmeCommand(profile compilerstyle.Profile) string {
	if profile != compilerstyle.ProfileJavaStructured {
		return ""
	}
	return "go tool " + StyleTool + " --config=.spice/style.json ./..."
}

func (styleScaffold) configurationContent() []byte {
	return []byte(`{
  "schemaVersion": 2,
  "profile": "java-structured",
  "sourceRoots": [
    "cmd",
    "internal"
  ],
  "generatedRoots": [
    "internal/spicegen"
  ],
  "buildSelections": [
    {
      "name": "linux-amd64-default",
      "sourceRoots": [
        "cmd",
        "internal"
      ],
      "goos": "linux",
      "goarch": "amd64",
      "cgoEnabled": false,
      "tags": []
    },
    {
      "name": "windows-amd64-default",
      "sourceRoots": [
        "cmd",
        "internal"
      ],
      "goos": "windows",
      "goarch": "amd64",
      "cgoEnabled": false,
      "tags": []
    }
  ],
  "rules": {
    "onePrimaryTypePerFile": "error",
    "methodsInPrimaryTypeFile": "error",
    "fileNameMatchesType": "error",
    "packageFunctions": "error",
    "explicitConstructors": "error",
    "explicitManagedScopes": "off",
    "banInit": "error",
    "banMutablePackageState": "error",
    "privateManagedFields": "off",
    "moduleOwnership": "off",
    "routeClassification": "off",
    "contextFirst": "error",
    "errorLast": "error",
    "maxTypeFileLines": 500
  },
  "publicRoutes": [],
  "allowedBoundaryFiles": [
    "**/doc.go",
    "**/main.go",
    "**/*_bean.go",
    "**/*_topic.go",
    "**/*_test.go",
    "internal/spicegen/**"
  ],
  "packageFunctionExceptions": [
    {
      "glob": "**/main.go",
      "symbol": "main",
      "reason": "Go process entrypoint"
    },
    {
      "glob": "**/*_bean.go",
      "contributionKind": "provider",
      "maximum": 1,
      "reason": "Exact Spice provider boundary"
    },
    {
      "glob": "**/*_topic.go",
      "contributionKind": "event-topic",
      "maximum": 1,
      "reason": "Typed Spice event topic marker"
    },
    {
      "glob": "**/*_test.go",
      "symbolPattern": "^(Test|Benchmark|Fuzz|Example|TestMain)",
      "reason": "Go testing entrypoint"
    }
  ],
  "packageVariableExceptions": []
}
`)
}
