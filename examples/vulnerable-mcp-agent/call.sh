#!/usr/bin/env bash
# Makes one MCP tools/call to a plane's stateless HTTP listener, as an agent
# does, and prints the JSON-RPC answer.
#
#   call.sh <mcp-url> <tool> '<arguments as a JSON object>'
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s <mcp-url> <tool> <arguments-json>\n' "$0" >&2
  exit 2
fi
url="$1"
tool="$2"
args="$3"
if [[ ! "${tool}" =~ ^[a-z_]+$ ]]; then
  printf '%s: a tool name is lower-case letters and underscores\n' "$0" >&2
  exit 2
fi

version="2026-07-28"
body='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"'"${tool}"'","arguments":'"${args}"',"_meta":{"io.modelcontextprotocol/protocolVersion":"'"${version}"'","io.modelcontextprotocol/clientCapabilities":{}}}}'

# The answer comes as JSON or as one server-sent event; either way the
# JSON-RPC message is printed alone.
answer="$(curl -sS --max-time 30 -X POST "${url}" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H "MCP-Protocol-Version: ${version}" \
  -H 'Mcp-Method: tools/call' \
  -H "Mcp-Name: ${tool}" \
  --data-binary "${body}")"
case "${answer}" in
  event:* | data:*) printf '%s\n' "${answer}" | sed -n 's/^data: //p' ;;
  *) printf '%s\n' "${answer}" ;;
esac
