package mermaid

import (
	"fmt"
	"math"
	"strings"
)

// box is a placed node, dummy or group: its centre and size.
type box struct {
	x, y, w, h float64
}

type drawing struct {
	boxes         map[string]*box
	groups        map[string]*box
	width, height float64
}

// axis reads sizes along the rank axis and across it, for one direction.
type axis struct {
	sizes    map[string][2]float64
	groups   map[string]string
	vertical bool
	gap      float64
}

func (a axis) along(item string) float64 {
	s := a.sizes[item]
	if a.vertical {
		return s[1]
	}
	return s[0]
}

func (a axis) across(item string) float64 {
	if strings.HasPrefix(item, "~") {
		return 8.0
	}
	s := a.sizes[item]
	if a.vertical {
		return s[0]
	}
	return s[1]
}

// place lays the layers out along one axis; compact tightens a drawing meant
// for a narrow screen.
func place(g *Graph, layers [][]string, vertical, compact bool) (*drawing, error) {
	ax := axis{sizes: map[string][2]float64{}, groups: map[string]string{}, vertical: vertical, gap: nodeGap}
	if compact {
		ax.gap = float64(nodeGap * 0.65)
	}
	for _, node := range g.Nodes {
		ax.sizes[node.ID] = size(node, compact)
		ax.groups[node.ID] = node.Group
	}
	gaps := rankGaps(g, layers, vertical)
	titles := map[string]float64{}
	for _, group := range g.Groups {
		titles[group.ID] = titleWidth(group.Title)
	}
	boxes := map[string]*box{}
	stacks := stackLayers(layers, ax, gaps, boxes, titles)
	widest := stacks[0]
	for _, s := range stacks[1:] {
		widest = max(widest, s)
	}
	for r, layer := range layers {
		shift := (widest - stacks[r]) / 2
		for _, item := range layer {
			if vertical {
				boxes[item].x += shift
			} else {
				boxes[item].y += shift
			}
		}
	}
	groupBoxes := map[string]*box{}
	for _, group := range g.Groups {
		groupBoxes[group.ID] = enclose(group, boxes)
	}
	if err := refuseOverlaps(g, boxes, groupBoxes); err != nil {
		return nil, err
	}
	return normalise(layers, g, boxes, groupBoxes), nil
}

// stackLayers places each layer's items at its position along the axis and
// returns the length each layer takes across it.
func stackLayers(layers [][]string, ax axis, gaps []float64, boxes map[string]*box, titles map[string]float64) []float64 {
	stacks := make([]float64, 0, len(layers))
	start := 0.0
	for r, layer := range layers {
		head, tail := 0.0, 0.0
		if ax.vertical && grouped(ax, layer) {
			head, tail = groupPad+groupTitle, groupPad
		}
		deepest := depth(ax, layer[0], titles)
		for _, item := range layer[1:] {
			deepest = max(deepest, depth(ax, item, titles))
		}
		stacks = append(stacks, stack(layer, ax, start+head+deepest/2, boxes, titles))
		gap := 0.0
		if r < len(gaps) {
			gap = gaps[r]
		}
		start += head + deepest + tail + gap
	}
	return stacks
}

func grouped(ax axis, layer []string) bool {
	for _, item := range layer {
		if ax.groups[item] != "" {
			return true
		}
	}
	return false
}

// depth is how much of the rank axis an item needs, its group's frame and
// title included.
func depth(ax axis, item string, titles map[string]float64) float64 {
	group := ax.groups[item]
	if ax.vertical || group == "" {
		return ax.along(item)
	}
	return max(ax.along(item)+float64(2*groupPad), titles[group])
}

// rankGaps widens the gap after a rank so that an edge label leaving it fits
// between the ranks.
func rankGaps(g *Graph, layers [][]string, vertical bool) []float64 {
	gaps := make([]float64, max(len(layers)-1, 0))
	for i := range gaps {
		gaps[i] = rankGap
	}
	for _, e := range g.Edges {
		if e.Label == "" {
			continue
		}
		need := labelH + 22
		if !vertical {
			need = textWidth(e.Label, true) + 28
		}
		first := rankOf(layers, e.From)
		gaps[first] = max(gaps[first], need)
	}
	return gaps
}

func rankOf(layers [][]string, item string) int {
	for r, layer := range layers {
		for _, i := range layer {
			if i == item {
				return r
			}
		}
	}
	return -1
}

// stack places one rank's items across the axis and returns the length they
// take. Across a vertical drawing a group's title runs along the stack, so a
// title wider than its members gets its overhang reserved on both sides.
func stack(layer []string, ax axis, centre float64, boxes map[string]*box, titles map[string]float64) float64 {
	cursor, overhang := 0.0, 0.0
	for index, item := range layer {
		group := ax.groups[item]
		opens := group != "" && (index == 0 || ax.groups[layer[index-1]] != group)
		closes := group != "" && (index == len(layer)-1 || ax.groups[layer[index+1]] != group)
		if opens {
			span, members := 0.0, 0
			for _, m := range layer {
				if ax.groups[m] == group {
					span += ax.across(m)
					members++
				}
			}
			span += float64(ax.gap * float64(members-1))
			wanted := titles[group] - span - float64(2*groupPad)
			overhang = 0.0
			title := groupTitle
			if ax.vertical {
				overhang = max(0.0, wanted) / 2
				title = 0.0
			}
			cursor += groupPad + overhang + title
		}
		across := ax.across(item)
		s := ax.sizes[item]
		middle := cursor + across/2
		if ax.vertical {
			boxes[item] = &box{middle, centre, s[0], s[1]}
		} else {
			boxes[item] = &box{centre, middle, s[0], s[1]}
		}
		closing := 0.0
		if closes {
			closing = groupPad + overhang
		}
		cursor += across + closing + ax.gap
	}
	return cursor - ax.gap
}

func titleWidth(title string) float64 {
	return float64(float64(float64(len([]rune(title)))*groupFont)*0.68) + float64(2*groupPad)
}

func enclose(group *Group, boxes map[string]*box) *box {
	left, right := math.Inf(1), math.Inf(-1)
	top, bottom := math.Inf(1), math.Inf(-1)
	for _, m := range group.Members {
		b := boxes[m]
		left = min(left, b.x-b.w/2)
		right = max(right, b.x+b.w/2)
		top = min(top, b.y-b.h/2)
		bottom = max(bottom, b.y+b.h/2)
	}
	left -= groupPad
	right += groupPad
	if short := titleWidth(group.Title) - (right - left); short > 0 {
		left, right = left-short/2, right+short/2
	}
	top = top - groupPad - groupTitle
	bottom += groupPad
	return &box{(left + right) / 2, (top + bottom) / 2, right - left, bottom - top}
}

// normalise moves the drawing so that its top left corner sits one margin
// from the origin, and measures it.
func normalise(layers [][]string, g *Graph, boxes, groups map[string]*box) *drawing {
	var items []*box
	for _, layer := range layers {
		for _, item := range layer {
			items = append(items, boxes[item])
		}
	}
	for _, group := range g.Groups {
		items = append(items, groups[group.ID])
	}
	minX, minY := math.Inf(1), math.Inf(1)
	for _, b := range items {
		minX = min(minX, b.x-b.w/2)
		minY = min(minY, b.y-b.h/2)
	}
	minX -= margin
	minY -= margin
	width, height := math.Inf(-1), math.Inf(-1)
	for _, b := range items {
		b.x -= minX
		b.y -= minY
		width = max(width, b.x+b.w/2)
		height = max(height, b.y+b.h/2)
	}
	return &drawing{boxes: boxes, groups: groups, width: width + margin, height: height + margin}
}

func overlap(a, b *box) bool {
	return math.Abs(a.x-b.x)*2 < a.w+b.w && math.Abs(a.y-b.y)*2 < a.h+b.h
}

func refuseOverlaps(g *Graph, boxes, groups map[string]*box) error {
	for index, group := range g.Groups {
		area := groups[group.ID]
		for _, node := range g.Nodes {
			if node.Group != group.ID && overlap(boxes[node.ID], area) {
				return fmt.Errorf("node %s would be drawn inside subgraph %s", node.ID, group.ID)
			}
		}
		for _, other := range g.Groups[index+1:] {
			if overlap(area, groups[other.ID]) {
				return fmt.Errorf("subgraphs %s and %s would overlap", group.ID, other.ID)
			}
		}
	}
	return nil
}
