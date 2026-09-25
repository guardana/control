// Package mcp is the Model Context Protocol adapter: the listener toward the
// agent, the client toward each upstream, the router, the translator from a
// call to an envelope, the manifest, and the shape of every answer the agent
// sees (ADR-0013). It translates; the meaning of a call lives in the envelope
// it builds and in the policy, never here.
package mcp
