package gatewayconfig

import "fmt"

// checkUpstreams refuses an upstream that names neither a URL nor a command,
// or both, arguments or variables without a command, and a variable a
// command may not receive.
func (c *Config) checkUpstreams() error {
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("upstreams: no upstream; the gateway has nothing to serve")
	}
	for i, up := range c.Upstreams {
		at := fmt.Sprintf("upstreams.%d.", i)
		if err := checkRequired(&c.Upstreams[i], upstreamFields, at); err != nil {
			return err
		}
		switch {
		case (up.Endpoint == "") == (up.Command == ""):
			return fmt.Errorf("%s: name either an endpoint or a command, not both and not neither", at[:len(at)-1])
		case up.Command == "" && len(up.Args) > 0:
			return fmt.Errorf("%sargs: arguments without a command", at)
		case up.Command == "" && len(up.Env) > 0:
			return fmt.Errorf("%senv: variables without a command; an HTTP upstream starts no process to pass them to", at)
		}
		if err := checkEnvNames(at+"env.", up.Env); err != nil {
			return err
		}
		if up.Endpoint != "" && !httpURL(up.Endpoint) {
			return notHTTP(at+"endpoint", up.Endpoint)
		}
	}
	return nil
}

// checkOverrides refuses a classification of a tool on a server this
// configuration does not have, which would classify nothing and leave the
// tool blocked.
func (c *Config) checkOverrides() error {
	names := map[string]bool{}
	for _, up := range c.Upstreams {
		names[up.Name] = true
	}
	for i := range c.Overrides {
		at := fmt.Sprintf("overrides.%d.", i)
		if err := checkRequired(&c.Overrides[i], overrideFields, at); err != nil {
			return err
		}
		if !names[c.Overrides[i].Upstream] {
			return fmt.Errorf("%supstream: %s is not a configured upstream", at, quoteValue(c.Overrides[i].Upstream))
		}
	}
	return nil
}
