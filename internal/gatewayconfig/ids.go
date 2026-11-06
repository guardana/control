package gatewayconfig

import (
	"fmt"

	"github.com/guardana/control/pkg/contract"
)

// checkIdentifiers refuses a project, tenant or environment, of the plane or
// of an upstream, that contract.CheckID refuses, naming the key: every
// envelope carries the plane's and every call to an upstream its own, so the
// contract would refuse each call instead. An empty optional key is unset;
// checkRequired has refused an empty required one.
func (c *Config) checkIdentifiers() error {
	type key struct{ path, value string }
	keys := []key{{"project_id", c.ProjectID}, {"tenant_id", c.TenantID}, {"environment", c.Environment}}
	for i, up := range c.Upstreams {
		keys = append(keys, key{fmt.Sprintf("upstreams.%d.tenant_id", i), up.TenantID},
			key{fmt.Sprintf("upstreams.%d.environment", i), up.Environment})
	}
	for _, k := range keys {
		if k.value == "" {
			continue
		}
		if err := contract.CheckID(k.value); err != nil {
			return fmt.Errorf("%s: %w", k.path, err)
		}
	}
	return nil
}
