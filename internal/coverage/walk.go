package coverage

// MaxWalkSpans bounds one observation's walk down its trace: its own span and
// every span below it. A walk past the bound is not checked, and the map has a
// gap, since an agent can nest spans below its own call to cut its walk.
const MaxWalkSpans = 1 << 16

// spanKey is a span of one trace among one tenant's project's observations.
type spanKey struct{ tenant, project, trace, span string }

// family indexes one source's observations by tenant, project, trace and
// parent span, so the descendants of a span are found without a scan per
// observation, and never among another tenant's or project's observations.
// It keeps what every walk learned, so a span is walked once however many
// observations sit above it.
type family struct {
	children map[spanKey][]string
	// group is the group of each span a walk reached.
	group  map[spanKey]int
	groups []spanGroup
}

// spanGroup is spans of one trace that each reach every other one: a single
// span unless the trace's parents loop.
type spanGroup struct {
	spans []string
	// below are the groups the group's spans are the parents of.
	below []int
	// size counts the spans at and below the group, held at MaxWalkSpans+1
	// once past it. A span reached by two ways is counted for each, which
	// only spans claiming two parents in one trace can do.
	size int
}

// families keeps each source's family for every path of a map.
type families map[*Source]*family

func (fs families) of(src *Source) *family {
	if f := fs[src]; f != nil {
		return f
	}
	f := &family{children: map[spanKey][]string{}, group: map[spanKey]int{}}
	for _, r := range src.Records {
		o := r.GetObservation()
		c := o.GetCorrelation()
		if o == nil || o.GetSource().GetSourceId() != src.Descriptor.GetSourceId() ||
			c.GetTraceId() == "" || c.GetParentSpanId() == "" || c.GetSpanId() == "" {
			continue
		}
		key := spanKey{o.GetTenantId(), o.GetProjectId(), c.GetTraceId(), c.GetParentSpanId()}
		f.children[key] = append(f.children[key], c.GetSpanId())
	}
	fs[src] = f
	return f
}

// walk returns the group of root, first grouping every span below it not yet
// grouped. It is Tarjan's strongly connected components, iterative so a deep
// trace cannot exhaust the stack; a group is complete when it is emitted, so
// its size is the sum of the groups below it.
func (f *family) walk(root spanKey) int {
	if g, ok := f.group[root]; ok {
		return g
	}
	type frame struct {
		key  spanKey
		next int
	}
	// A span indexed and not yet grouped is on stack.
	index, low := map[spanKey]int{}, map[spanKey]int{}
	var stack []spanKey
	var calls []frame
	visit := func(k spanKey) {
		index[k], low[k] = len(index), len(index)
		stack = append(stack, k)
		calls = append(calls, frame{key: k})
	}
	visit(root)
	for len(calls) > 0 {
		top := &calls[len(calls)-1]
		if kids := f.children[top.key]; top.next < len(kids) {
			child := top.key
			child.span = kids[top.next]
			top.next++
			if _, grouped := f.group[child]; grouped {
				continue
			}
			if i, seen := index[child]; seen {
				low[top.key] = min(low[top.key], i)
				continue
			}
			visit(child)
			continue
		}
		k := top.key
		calls = calls[:len(calls)-1]
		if n := len(calls); n > 0 {
			parent := calls[n-1].key
			low[parent] = min(low[parent], low[k])
		}
		if low[k] != index[k] {
			continue
		}
		var members []spanKey
		for {
			m := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			f.group[m] = len(f.groups)
			members = append(members, m)
			if m == k {
				break
			}
		}
		f.groups = append(f.groups, f.newGroup(members))
	}
	return f.group[root]
}

// newGroup makes the group of members, every span below them already grouped.
func (f *family) newGroup(members []spanKey) spanGroup {
	id := f.group[members[0]]
	g := spanGroup{size: len(members)}
	taken := map[int]bool{id: true}
	for _, m := range members {
		g.spans = append(g.spans, m.span)
		for _, kid := range f.children[m] {
			child := m
			child.span = kid
			b := f.group[child]
			if taken[b] {
				continue
			}
			taken[b] = true
			g.below = append(g.below, b)
			g.size = min(g.size+f.groups[b].size, MaxWalkSpans+1)
		}
	}
	return g
}
