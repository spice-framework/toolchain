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
  "schemaVersion": 1,
  "profile": "java-structured",
  "sourceRoots": [
    "cmd",
    "internal"
  ],
  "generatedRoots": [
    "internal/spicegen"
  ],
  "rules": {
    "onePrimaryTypePerFile": "error",
    "methodsInPrimaryTypeFile": "error",
    "fileNameMatchesType": "error",
    "packageFunctions": "error",
    "explicitConstructors": "error",
    "explicitManagedScopes": "error",
    "banInit": "error",
    "banMutablePackageState": "error",
    "privateManagedFields": "error",
    "moduleOwnership": "error",
    "routeClassification": "error",
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
  ]
}
`)
}
