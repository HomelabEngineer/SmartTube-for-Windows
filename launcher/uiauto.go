package main

import (
	"html"
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
