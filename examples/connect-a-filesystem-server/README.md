# A plane in front of the reference filesystem server

`plane.yaml` puts the plane between an MCP client and
`@modelcontextprotocol/server-filesystem` 2026.8.31: it starts the server,
classifies its 14 tools (ten reads, four writes) and serves the client over
HTTP under the approval-for-writes starter pack, so every write waits for a
person. Fill in the two key lines and the directory the server may touch, as
[docs/guides/connect-an-existing-mcp-setup.md](../../docs/guides/connect-an-existing-mcp-setup.md)
says. Another version of the server lists other definitions, and its tools
are unclassified until you take their fingerprints again.
