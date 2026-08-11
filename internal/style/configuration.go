package style

import compilerstyle "github.com/spice-framework/toolchain/compiler/style"

const maximumConfigurationBytes = 1 << 20

// Configuration is the shared compiler-owned schema-two style contract.
type Configuration = compilerstyle.Configuration

// LoadConfiguration uses the same strict decoder as the typed compiler phase.
func LoadConfiguration(path string) (Configuration, error) {
	return compilerstyle.LoadConfiguration(path)
}
