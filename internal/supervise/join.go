package supervise

import (
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
)

// MaxJoinSteps bounds the span steps one supervision walks to join
// observations to plane calls. An observation the walk did not reach before
// the bound is in doubt, never taken as unjoined.
const MaxJoinSteps = 1 << 20

// noStep marks an entry of the procedure that is allowed, not a step.
const noStep = -1

// entry is a step or an allowed tool, as a call maps to it.
type entry struct {
	tool [2]string
	step int
}

// index maps a plane call's tool and upstream, and a name a source reports,
// to the entry they name.
type index struct {
	byTool map[[2]string]entry
	byName map[string]entry
}

func indexOf(p *Procedure) index {
	ix := index{byTool: map[[2]string]entry{}, byName: map[string]entry{}}
	add := func(e entry, names []string) {
		ix.byTool[e.tool] = e
		for _, n := range names {
			ix.byName[n] = e
		}
	}
	for i, s := range p.steps {
		add(entry{tool: [2]string{s.Tool, s.Upstream}, step: i}, s.ObservedAs)
	}
	for _, a := range p.allow {
		add(entry{tool: [2]string{a.Tool, a.Upstream}, step: noStep}, a.ObservedAs)
	}
	return ix
}

// callKey is what of a plane call an observation must match to be that
// call: the tool and upstream, and the tenant and project of the event and of
// its envelope.
type callKey struct {
	tool                                       [2]string
	evTenant, envTenant, evProject, envProject string
}

// start is a proposal's place in a trace, and the key of its call.
type start struct {
	trace, span string
	key         int
}

// spanGraph is one source's spans, each trace's apart, with the spans each is
// a child of, and the call keys marked at or below each.
type spanGraph struct {
	node   map[[2]string]int
	trace  []string
	up     [][]int
	keysAt [][]int
	seen   map[[2]int]bool
	// cut holds the traces a walk ran out of steps in.
	cut map[string]bool
}

// graphOf builds a source's graph from obs, the observations of the run it
// reported, so no observation left out of the run can join one that is in.
func graphOf(obs []*observev1.Observation) *spanGraph {
	g := &spanGraph{node: map[[2]string]int{}, seen: map[[2]int]bool{}, cut: map[string]bool{}}
	edges := map[[2]int]bool{}
	for _, o := range obs {
		c := o.GetCorrelation()
		if c.GetTraceId() == "" || c.GetSpanId() == "" {
			continue
		}
		child := g.add(c.GetTraceId(), c.GetSpanId())
		if c.GetParentSpanId() == "" {
			continue
		}
		parent := g.add(c.GetTraceId(), c.GetParentSpanId())
		if !edges[[2]int{child, parent}] {
			edges[[2]int{child, parent}] = true
			g.up[child] = append(g.up[child], parent)
		}
	}
	g.keysAt = make([][]int, len(g.up))
	return g
}

func (g *spanGraph) add(trace, span string) int {
	k := [2]string{trace, span}
	if n, ok := g.node[k]; ok {
		return n
	}
	n := int(len(g.up))
	g.node[k] = n
	g.trace = append(g.trace, trace)
	g.up = append(g.up, nil)
	return n
}

// joiner finds the plane call an observation is, as coverage joins them: a
// proposal of the run with the observation's trace id and its own or a
// descendant's span id, in its tenant and project, for the tool the
// observation names. An observation names a tool by the plane's name for it,
// or by a name an entry of the procedure lists. Nothing joins on an empty id.
//
// Each proposal marks its key on its span and every ancestor span once, so
// the walk is linear in the spans times the distinct calls of a trace, and
// MaxJoinSteps bounds it.
type joiner struct {
	ix     index
	keys   []callKey
	starts []start
	steps  int
	spans  map[*Source][]*observev1.Observation
	graphs map[*Source]*spanGraph
}

func newJoiner(ix index, reqs []*request, spans map[*Source][]*observev1.Observation) *joiner {
	j := &joiner{ix: ix, spans: spans, graphs: map[*Source]*spanGraph{}}
	keyID := map[callKey]int{}
	seen := map[start]bool{}
	for _, rq := range reqs {
		ev, env := rq.proposal, rq.proposal.GetProposed()
		if env.GetTraceId() == "" || env.GetSpanId() == "" {
			continue
		}
		tool, _ := rq.tool()
		k := callKey{tool: tool, evTenant: ev.GetTenantId(), envTenant: env.GetTenantId(),
			evProject: ev.GetProjectId(), envProject: env.GetProjectId()}
		id, ok := keyID[k]
		if !ok {
			id = int(len(j.keys))
			keyID[k] = id
			j.keys = append(j.keys, k)
		}
		if st := (start{trace: env.GetTraceId(), span: env.GetSpanId(), key: id}); !seen[st] {
			seen[st] = true
			j.starts = append(j.starts, st)
		}
	}
	return j
}

// joined reports whether o is a plane call, and when it is not, whether a
// walk that ran out of steps leaves that in doubt.
func (j *joiner) joined(o *observev1.Observation, src *Source) (bool, bool) {
	trace, span := o.GetCorrelation().GetTraceId(), o.GetCorrelation().GetSpanId()
	if trace == "" || span == "" {
		return false, false
	}
	g := j.graph(src)
	if n, ok := g.node[[2]string{trace, span}]; ok {
		for _, k := range g.keysAt[n] {
			if j.sameCall(o, j.keys[k]) {
				return true, false
			}
		}
	}
	return false, g.cut[trace]
}

func (j *joiner) graph(src *Source) *spanGraph {
	if g, ok := j.graphs[src]; ok {
		return g
	}
	g := graphOf(j.spans[src])
	j.graphs[src] = g
	for _, st := range j.starts {
		if n, ok := g.node[[2]string{st.trace, st.span}]; ok {
			j.walk(g, n, st.key)
		}
	}
	return g
}

// walk marks key on n and every span above it not yet marked with it.
func (j *joiner) walk(g *spanGraph, n, key int) {
	stack := []int{n}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if g.seen[[2]int{n, key}] {
			continue
		}
		if j.steps >= MaxJoinSteps {
			g.cut[g.trace[n]] = true
			return
		}
		j.steps++
		g.seen[[2]int{n, key}] = true
		g.keysAt[n] = append(g.keysAt[n], key)
		stack = append(stack, g.up[n]...)
	}
}

func (j *joiner) sameCall(o *observev1.Observation, k callKey) bool {
	name := o.GetSubject().GetName()
	named := k.tool[0] == name
	if e, ok := j.ix.byName[name]; ok && e.tool == k.tool {
		named = true
	}
	return named && k.evTenant == o.GetTenantId() && k.envTenant == o.GetTenantId() &&
		k.evProject == o.GetProjectId() && k.envProject == o.GetProjectId()
}
