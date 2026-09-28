package mermaid

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// The drawing's dimensions are variables, not constants: a constant
// expression is evaluated exactly at compile time, and every coordinate has
// to round as float64 arithmetic does at each step. For the same reason each
// product is wrapped in float64(), which stops the compiler fusing it with an
// addition into one differently rounded multiply-add.
var (
	fontSize   = 13.0
	monoSize   = 12.5
	lineHeight = 18.0
	padX       = 16.0
	padY       = 10.0
	nodeGap    = 18.0
	rankGap    = 56.0
	groupPad   = 14.0
	groupTitle = 20.0
	groupFont  = 11.0
	labelH     = 18.0
	docLip     = 6.0
	margin     = 4.0
)

func textWidth(text string, mono bool) float64 {
	if mono {
		return float64(float64(float64(len([]rune(text)))*monoSize) * 0.6)
	}
	width := 0.0
	for _, r := range text {
		switch {
		case strings.ContainsRune("il.,:;'|!·", r):
			width += 0.3
		case strings.ContainsRune("mwMW", r):
			width += 0.86
		case isUpper(r):
			width += 0.66
		case r == ' ':
			width += 0.28
		default:
			width += 0.55
		}
	}
	return float64(width * fontSize)
}

// size is a node's width and height, each rounded half to even.
func size(node *Node, compact bool) [2]float64 {
	mono := slices.Contains(node.Classes, "cmd")
	widest := textWidth(node.Lines[0], mono)
	for _, line := range node.Lines[1:] {
		widest = max(widest, textWidth(line, mono))
	}
	pad := padX
	if compact {
		pad = float64(padX * 0.65)
	}
	pill := 0.0
	if node.Shape == "pill" {
		pill = float64(lineHeight * 0.6)
	}
	lip := 0.0
	if node.Shape == "doc" {
		lip = docLip
	}
	width := max(64.0, widest+float64(2*pad)+pill)
	height := float64(float64(len(node.Lines))*lineHeight) + float64(2*padY) + lip
	return [2]float64{math.RoundToEven(width), math.RoundToEven(height)}
}

// ranks is the longest path from the sources, with each source pulled next
// to its nearest target.
func ranks(g *Graph) (map[string]int, error) {
	rank, err := longestPaths(g)
	if err != nil {
		return nil, err
	}
	for _, node := range g.Nodes {
		if slices.ContainsFunc(g.Edges, func(e Edge) bool { return e.To == node.ID }) {
			continue
		}
		nearest, found := 0, false
		for _, e := range g.Edges {
			if e.From == node.ID && (!found || rank[e.To] < nearest) {
				nearest, found = rank[e.To], true
			}
		}
		if found {
			rank[node.ID] = nearest - 1
		}
	}
	for _, group := range g.Groups {
		for _, member := range group.Members[1:] {
			if rank[member] != rank[group.Members[0]] {
				return nil, fmt.Errorf("subgraph %s spans several ranks; keep a group in one", group.ID)
			}
		}
	}
	return rank, nil
}

func longestPaths(g *Graph) (map[string]int, error) {
	incoming := map[string]int{}
	for _, e := range g.Edges {
		incoming[e.To]++
	}
	rank := map[string]int{}
	var ready []string
	for _, node := range g.Nodes {
		rank[node.ID] = 0
		if incoming[node.ID] == 0 {
			ready = append(ready, node.ID)
		}
	}
	seen := 0
	for len(ready) > 0 {
		node := ready[0]
		ready = ready[1:]
		seen++
		for _, e := range g.Edges {
			if e.From != node {
				continue
			}
			rank[e.To] = max(rank[e.To], rank[node]+1)
			incoming[e.To]--
			if incoming[e.To] == 0 {
				ready = append(ready, e.To)
			}
		}
	}
	if seen != len(g.Nodes) {
		return nil, errors.New("the diagram has a cycle; draw a directed acyclic graph")
	}
	return rank, nil
}

// layers returns the rank layers, a dummy for each rank an edge skips, and
// each edge's chain of items through them. Four sweeps down and up order each
// layer by the mean position of its neighbours.
func layers(g *Graph, rank map[string]int) ([][]string, [][]string) {
	top := 0
	for _, r := range rank {
		top = max(top, r)
	}
	out := make([][]string, top+1)
	for _, node := range g.Nodes {
		out[rank[node.ID]] = append(out[rank[node.ID]], node.ID)
	}
	chains := make([][]string, 0, len(g.Edges))
	up, down := map[string][]string{}, map[string][]string{}
	for index, e := range g.Edges {
		chain := []string{e.From}
		for step := rank[e.From] + 1; step < rank[e.To]; step++ {
			dummy := "~" + strconv.Itoa(index) + "~" + strconv.Itoa(step)
			out[step] = append(out[step], dummy)
			chain = append(chain, dummy)
		}
		chain = append(chain, e.To)
		chains = append(chains, chain)
		for i := 1; i < len(chain); i++ {
			down[chain[i-1]] = append(down[chain[i-1]], chain[i])
			up[chain[i]] = append(up[chain[i]], chain[i-1])
		}
	}
	groups := map[string]string{}
	for _, node := range g.Nodes {
		groups[node.ID] = node.Group
	}
	for range 4 {
		for r := 1; r < len(out); r++ {
			out[r] = order(out[r], out[r-1], up, groups)
		}
		for r := len(out) - 2; r >= 0; r-- {
			out[r] = order(out[r], out[r+1], down, groups)
		}
	}
	return out, chains
}

// order sorts a layer by the mean position of its neighbours in the other
// layer, keeping each group together at the mean of its members.
func order(layer, other []string, links map[string][]string, groups map[string]string) []string {
	where := map[string]int{}
	for index, item := range other {
		where[item] = index
	}
	centre := map[string]float64{}
	for index, item := range layer {
		total, count := 0, 0
		for _, n := range links[item] {
			if at, ok := where[n]; ok {
				total += at
				count++
			}
		}
		centre[item] = float64(index)
		if count > 0 {
			centre[item] = float64(total) / float64(count)
		}
	}
	anchor := map[string]float64{}
	for _, item := range layer {
		var members []float64
		for _, i := range layer {
			if groups[item] != "" && groups[i] == groups[item] {
				members = append(members, centre[i])
			}
		}
		if len(members) == 0 {
			members = []float64{centre[item]}
		}
		anchor[item] = compensatedSum(members) / float64(len(members))
	}
	sorted := slices.Clone(layer)
	slices.SortStableFunc(sorted, func(a, b string) int {
		return cmp.Or(cmp.Compare(anchor[a], anchor[b]), cmp.Compare(groups[a], groups[b]),
			cmp.Compare(centre[a], centre[b]))
	})
	return sorted
}

// compensatedSum adds with Neumaier's compensation, applied once at the end.
// A plain sum rounds differently, and a group's anchor that ties another
// item's exactly would then sort on the wrong side of it.
func compensatedSum(values []float64) float64 {
	total := 0.0 + values[0]
	compensation := 0.0
	for _, x := range values[1:] {
		t := total + x
		if math.Abs(total) >= math.Abs(x) {
			compensation += (total - t) + x
		} else {
			compensation += (x - t) + total
		}
		total = t
	}
	if compensation != 0 && !math.IsInf(compensation, 0) && !math.IsNaN(compensation) {
		total += compensation
	}
	return total
}
