package main

import (
	"html"
	"image"
	"regexp"
	"strconv"
	"strings"
)

// Minimal reader for `uiautomator dump` XML, used to switch on SmartTube
// settings that apps keep in private storage.

type uiNode struct {
	Text      string
	Class     string
	Checkable bool
	Checked   bool
	Focused   bool
	X1, Y1    int
	X2, Y2    int
}

func (n uiNode) center() (int, int) { return (n.X1 + n.X2) / 2, (n.Y1 + n.Y2) / 2 }

var (
	uiNodeRe   = regexp.MustCompile(`<node [^>]*>`)
	uiAttrRe   = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)
	uiBoundsRe = regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)
)

func parseUINodes(xml string) []uiNode {
	var out []uiNode
	for _, tag := range uiNodeRe.FindAllString(xml, -1) {
		var n uiNode
		for _, m := range uiAttrRe.FindAllStringSubmatch(tag, -1) {
			switch m[1] {
			case "text":
				n.Text = html.UnescapeString(m[2])
			case "class":
				n.Class = m[2]
			case "checkable":
				n.Checkable = m[2] == "true"
			case "checked":
				n.Checked = m[2] == "true"
			case "focused":
				n.Focused = n.Focused || m[2] == "true"
			case "bounds":
				if b := uiBoundsRe.FindStringSubmatch(m[2]); b != nil {
					n.X1, _ = strconv.Atoi(b[1])
					n.Y1, _ = strconv.Atoi(b[2])
					n.X2, _ = strconv.Atoi(b[3])
					n.Y2, _ = strconv.Atoi(b[4])
				}
			}
		}
		out = append(out, n)
	}
	return out
}

// findText returns the node whose text is exactly t, or failing that the
// first node whose text contains t (labels may carry a second line, e.g.
// "Codec selection: prefer avc over vp9\nWARN: 1080p maximum").
func findText(nodes []uiNode, t string) (uiNode, bool) {
	for _, n := range nodes {
		if n.Text == t && n.X2 > n.X1 && n.Y2 > n.Y1 {
			return n, true
		}
	}
	lt := strings.ToLower(t)
	for _, n := range nodes {
		if strings.Contains(strings.ToLower(n.Text), lt) && n.X2 > n.X1 && n.Y2 > n.Y1 {
			return n, true
		}
	}
	return uiNode{}, false
}

// checkStateNear reports the checked state of the checkable node on the same
// row as n (vertical overlap). ok=false if there is none.
func checkStateNear(nodes []uiNode, n uiNode) (checked, ok bool) {
	for _, c := range nodes {
		isBox := c.Checkable || strings.Contains(c.Class, "CheckBox") || strings.Contains(c.Class, "Checked") || strings.Contains(c.Class, "Switch")
		if isBox && c.Y1 < n.Y2 && c.Y2 > n.Y1 {
			return c.Checked, true
		}
	}
	// The row itself may be the checkable view.
	for _, c := range nodes {
		if c.Checked && c.Y1 <= n.Y1 && c.Y2 >= n.Y2 {
			return true, true
		}
	}
	return false, false
}

// focusedTop returns the top edge of the focused view (-1 if none).
func focusedTop(nodes []uiNode) int {
	top := -1
	for _, n := range nodes {
		if n.Focused && (top < 0 || n.Y1 < top) {
			top = n.Y1
		}
	}
	return top
}

// anyFocused reports whether some view has focus.
func anyFocused(nodes []uiNode) bool {
	for _, n := range nodes {
		if n.Focused {
			return true
		}
	}
	return false
}

// focusedOn reports whether the focused view contains the node with text t.
func focusedOn(nodes []uiNode, t string) bool {
	n, ok := findText(nodes, t)
	if !ok {
		return false
	}
	if n.Focused {
		return true
	}
	x, y := n.center()
	for _, f := range nodes {
		if f.Focused && x >= f.X1 && x <= f.X2 && y >= f.Y1 && y <= f.Y2 {
			return true
		}
	}
	return false
}

// focusedRow returns the focused view that contains the node labelled t.
func focusedRow(nodes []uiNode, t string) (row, text uiNode, ok bool) {
	text, ok = findText(nodes, t)
	if !ok {
		return
	}
	x, y := text.center()
	for _, f := range nodes {
		if f.Focused && x >= f.X1 && x <= f.X2 && y >= f.Y1 && y <= f.Y2 {
			return f, text, true
		}
	}
	return uiNode{}, text, false
}

// checkStateFromImage reads a tickbox from a screenshot when uiautomator
// can't report it. The box sits in the row to the left of the label: find its
// left and right edges along the row's middle line, then compare the inside
// with the background just outside the box. An empty box's inside matches the
// background; a ticked box's inside is filled or crossed by the tick.
func checkStateFromImage(img image.Image, row, text uiNode) (checked, ok bool) {
	lum := func(x, y int) int {
		r, g, b, _ := img.At(x, y).RGBA()
		return int((299*r + 587*g + 114*b) / 1000 >> 8)
	}
	b := img.Bounds()
	cy := (row.Y1 + row.Y2) / 2
	if cy < b.Min.Y || cy >= b.Max.Y {
		return false, false
	}
	x0, x1 := max(row.X1, b.Min.X), min(text.X1, b.Max.X)
	bg := lum(x0+2, cy)
	left, right := -1, -1
	for x := x0; x < x1; x++ {
		if d := lum(x, cy) - bg; d > 60 || d < -60 {
			if left < 0 {
				left = x
			}
			right = x
		}
	}
	if left < 0 || right-left < 12 {
		return false, false // no box found
	}
	// Edges of an outline are a few pixels thick; look inside them.
	w := right - left
	in0, in1 := left+w/4, right-w/4
	marked, total := 0, 0
	for y := cy - w/4; y <= cy+w/4; y++ {
		for x := in0; x <= in1; x++ {
			total++
			if d := lum(x, y) - bg; d > 50 || d < -50 {
				marked++
			}
		}
	}
	if total == 0 {
		return false, false
	}
	return marked*100/total > 15, true
}
