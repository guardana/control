package mcp

// ForceSessionCap sets a's cap on sessions past New's checks, so a test can
// show the cap is not applied where New refuses one.
func ForceSessionCap(a *Adapter, n int) { a.cfg.Listener.MaxSessions = n }
