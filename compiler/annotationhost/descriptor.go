package annotationhost

import (
	"fmt"
	"slices"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/sdk"
)

// ValidateDescriptor proves that one statically decoded descriptor and its
// declared handler belong to this client's standard Go-resolved tool.
func (client *Client) ValidateDescriptor(
	descriptorPackage string,
	definition sdk.Definition,
	provenance annotation.ModuleProvenance,
) error {
	if client == nil {
		return fmt.Errorf(
			"validate annotation descriptor %q: tool client is nil",
			definition.Name,
		)
	}
	if err := ValidateDescriptorToolModule(
		provenance,
		client.Provenance(),
	); err != nil {
		return err
	}
	if !slices.Contains(
		client.DescriptorPackages(),
		descriptorPackage,
	) {
		return fmt.Errorf(
			"annotation descriptor %q package %q is not declared by its tool",
			definition.Name,
			descriptorPackage,
		)
	}
	expected := definition.Implementation
	for _, handler := range client.Handlers() {
		if handler.ID != expected.Handler {
			continue
		}
		if handler.Source != expected.Source {
			return fmt.Errorf(
				"annotation descriptor %q handler %q source is %s.%s, tool reports %s.%s",
				definition.Name,
				expected.Handler,
				expected.Source.Package,
				expected.Source.Name,
				handler.Source.Package,
				handler.Source.Name,
			)
		}
		return nil
	}
	return fmt.Errorf(
		"annotation descriptor %q requires missing handler %q",
		definition.Name,
		expected.Handler,
	)
}
