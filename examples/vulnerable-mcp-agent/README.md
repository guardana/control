# A vulnerable MCP agent

The demo the [tutorial](../../docs/get-started/try-the-demo.md) walks
through: two MCP servers an agent should not be trusted with, the plane's
configuration and policy in front of them, and seven scenarios.

A scenario is the agent's script: the gateway's `dev --scenario` command
plays it against the plane as an MCP client and checks every answer,
decision and trail.

| File | Holds |
| --- | --- |
| `main.go`, `servers.go` | One binary, `--serve orders` or `--serve web`, that the plane starts as an upstream over standard input and output. `orders` has `read_order`, `update_order`, `refund` and `export_orders`; `web` has `fetch_page`, whose page asks the agent to mail an order away, and `send_mail`. |
| `decisionpoint.go` | With `--decision-point`, the orders server also answers as an AuthZEN decision point on that loopback address: it publishes its metadata and never answers a question. |
| `journal.go` | What each server received. With `VICTIM_JOURNAL_DIR` set, each appends it to `orders.jsonl` or `web.jsonl` there, which is how the test sees that a blocked call never arrived. |
| `demo.yaml` | The plane: `ENFORCE`, both servers started from `bin/` at the repository's root, every tool classified, the decision point. `dev` sets the addresses, the key, the state and the approvals. |
| `policy.json` | Reads run; updates need an approval; refunds are denied; an export is asked of the decision point, whose silence blocks it; mail to an untrusted destination is blocked once the run has read untrusted text. |
| `scenarios/` | `allow`, `deny`, `approval`, `digest-invalidation`, `fail-closed`, `pause` and `toxic-flow`. |
| `call.sh` | One tool call by hand, as an agent makes it. |

The orders server binds `127.0.0.1:18213` for the decision point, so one demo
runs at a time. On a Unix-like system the tests run every scenario and the tutorial's calls, read
the journals, and fail one mutant of each scenario on purpose.
