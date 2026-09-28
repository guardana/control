package mermaid

// style is the CSS every figure carries. Each colour reads a page variable
// first, so the drawing follows the page's light and dark themes, and falls
// back to a value of its own on a page that sets none.
const style = "<style>" +
	".dg{margin:28px 0 32px;container-type:inline-size;overflow-x:auto}" +
	".dg svg{display:block;height:auto;margin:0 auto}" +
	".dg .dg-n{display:none}" +
	".dg text{text-anchor:middle;dominant-baseline:central}" +
	".dg .n rect,.dg .n path{fill:var(--panel,#fff);stroke:var(--line,#E6E8EF);stroke-width:1.2}" +
	".dg .n .lip{fill:none}" +
	".dg .n text{fill:var(--ink,#0F1115);font:500 13px var(--sans,system-ui,sans-serif)}" +
	".dg .accent rect,.dg .accent path{fill:var(--brand-soft,#E6F4F2);stroke:var(--brand,#0B8F80)}" +
	".dg .accent .lip{fill:none}" +
	".dg .accent text{fill:var(--brand-ink,#08705F)}" +
	".dg .cmd text{font:400 12.5px var(--mono,ui-monospace,monospace)}" +
	".dg .muted rect,.dg .muted path{stroke-dasharray:4 3}" +
	".dg .muted text{fill:var(--muted,#566070)}" +
	".dg .e{fill:none;stroke:var(--faint,#8A92A0);stroke-width:1.4}" +
	".dg .e.dot{stroke-dasharray:3 4}" +
	".dg .e.main{stroke:var(--brand,#0B8F80);stroke-width:1.8;stroke-dasharray:7 5;" +
	"animation:dg-flow 1.4s linear infinite}" +
	".dg .h{fill:var(--faint,#8A92A0)}.dg .h.main{fill:var(--brand,#0B8F80)}" +
	".dg .g rect{fill:color-mix(in srgb,var(--brand-soft,#E6F4F2) 45%,transparent);" +
	"stroke:var(--line,#E6E8EF)}" +
	".dg .g text{text-anchor:start;font:500 11px var(--mono,ui-monospace,monospace);" +
	"letter-spacing:.08em;text-transform:uppercase;fill:var(--muted,#566070)}" +
	".dg .l rect{fill:var(--bg,#FBFBFD)}" +
	".dg .l text{font:400 11.5px var(--mono,ui-monospace,monospace);fill:var(--muted,#566070)}" +
	"@keyframes dg-flow{to{stroke-dashoffset:-24}}" +
	"@media (prefers-reduced-motion:reduce){.dg .e.main{animation:none}}" +
	"</style>"
