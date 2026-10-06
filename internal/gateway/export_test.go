package gateway

// SetStops replaces p's stop source after New, which reaches a plane New
// refuses: one that serves local runs under a stop list.
func SetStops(p *Pipeline, s StopSource) { p.cfg.Stops = s }
