package icons

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

// The SVG reader is the part of the format a mark is drawn with, and nothing
// else: paths, the basic shapes, groups, transforms, solid fills and strokes.
// Everything a mark does not need but a renderer would get wrong -- gradients,
// filters, clipping, masks, text, references -- is refused by name, so that a
// drawing that would come out different from what its author saw is an error
// rather than a quiet change.

// svgNominal is the length, in pixels, of the longer side of a drawing's
// natural size. A vector has no size of its own; this is the one the rest of
// the package measures it in.
const svgNominal = 1024

// point is a position in the drawing's coordinates.
type point struct{ x, y float64 }

// matrix is the SVG transform [a b c d e f].
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) apply(p point) point {
	return point{m[0]*p.x + m[2]*p.y + m[4], m[1]*p.x + m[3]*p.y + m[5]}
}

// mul is m followed by n: the result applies n first, then m, which is the
// order a transform nested in a group composes in.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[2]*n[1], m[1]*n[0] + m[3]*n[1],
		m[0]*n[2] + m[2]*n[3], m[1]*n[2] + m[3]*n[3],
		m[0]*n[4] + m[2]*n[5] + m[4], m[1]*n[4] + m[3]*n[5] + m[5],
	}
}

// scale is the factor lengths grow by, which is what a stroke's width follows.
func (m matrix) scale() float64 {
	return math.Sqrt(math.Abs(m[0]*m[3] - m[1]*m[2]))
}

// segment is a straight line (to) or a cubic curve (c1, c2, to).
type segment struct {
	cubic      bool
	c1, c2, to point
}

// subpath is one contour: where it starts and what follows.
type subpath struct {
	start  point
	segs   []segment
	closed bool
}

func (s subpath) transform(m matrix) subpath {
	out := subpath{start: m.apply(s.start), closed: s.closed, segs: make([]segment, len(s.segs))}
	for i, g := range s.segs {
		out.segs[i] = segment{cubic: g.cubic, c1: m.apply(g.c1), c2: m.apply(g.c2), to: m.apply(g.to)}
	}
	return out
}

// reversed is the same contour drawn the other way round.
func (s subpath) reversed() subpath {
	out := subpath{closed: s.closed}
	if len(s.segs) == 0 {
		out.start = s.start
		return out
	}
	out.start = s.segs[len(s.segs)-1].to
	for i := len(s.segs) - 1; i >= 0; i-- {
		g := s.segs[i]
		from := s.start
		if i > 0 {
			from = s.segs[i-1].to
		}
		out.segs = append(out.segs, segment{cubic: g.cubic, c1: g.c2, c2: g.c1, to: from})
	}
	return out
}

// polyline is the contour as straight lines, curves cut into short pieces.
// tolerance is the length, in the contour's own units, one piece may be.
func (s subpath) polyline(piece float64) []point {
	pts := []point{s.start}
	cur := s.start
	for _, g := range s.segs {
		if !g.cubic {
			pts = append(pts, g.to)
			cur = g.to
			continue
		}
		span := dist(cur, g.c1) + dist(g.c1, g.c2) + dist(g.c2, g.to)
		n := int(math.Ceil(span / piece))
		n = min(max(n, 4), 96)
		for i := 1; i <= n; i++ {
			pts = append(pts, cubicAt(cur, g.c1, g.c2, g.to, float64(i)/float64(n)))
		}
		cur = g.to
	}
	return pts
}

func dist(a, b point) float64 { return math.Hypot(a.x-b.x, a.y-b.y) }

func cubicAt(p0, p1, p2, p3 point, t float64) point {
	u := 1 - t
	a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return point{a*p0.x + b*p1.x + c*p2.x + d*p3.x, a*p0.y + b*p1.y + c*p2.y + d*p3.y}
}

// paint is a fill or a stroke.
type paint struct {
	none bool
	col  color.NRGBA
}

// style is what an element inherits and what it sets.
type style struct {
	fill, stroke             paint
	strokeWidth              float64
	opacity                  float64
	fillOpacity, strokeAlpha float64
	evenOdd                  bool
	cap, join                string
	miterLimit               float64
	hidden                   bool
}

func defaultStyle() style {
	return style{
		fill:        paint{col: color.NRGBA{A: 255}},
		stroke:      paint{none: true},
		strokeWidth: 1,
		opacity:     1,
		fillOpacity: 1,
		strokeAlpha: 1,
		cap:         "butt",
		join:        "miter",
		miterLimit:  4,
	}
}

// shape is one drawn element, its contours already in the drawing's space.
type shape struct {
	paths []subpath
	st    style
	// width is the stroke's width in the drawing's space.
	width float64
}

// svgImage is a parsed drawing. It implements Image.
type svgImage struct {
	vbX, vbY, vbW, vbH float64
	k                  float64 // pixels of natural size per unit of the drawing
	w, h               int
	shapes             []shape
}

// refused are the elements that change how something looks in a way this
// reader does not follow.
var refused = map[string]bool{
	"linearGradient": true, "radialGradient": true, "pattern": true, "filter": true,
	"clipPath": true, "mask": true, "text": true, "image": true, "use": true,
	"foreignObject": true, "style": true, "marker": true, "symbol": true,
}

// skipped are the elements that carry no picture.
var skipped = map[string]bool{
	"title": true, "desc": true, "metadata": true, "defs": true, "namedview": true,
}

// parseSVG reads a drawing.
func parseSVG(data []byte) (*svgImage, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	type frame struct {
		st style
		m  matrix
	}
	var (
		stack []frame
		img   *svgImage
		depth int // depth of the element being skipped
		skip  bool
	)

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the SVG is not well-formed: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			tag := t.Name.Local
			if skip {
				depth++
				continue
			}
			if img == nil {
				if tag != "svg" {
					return nil, fmt.Errorf("the file is not an SVG: it opens with <%s>", tag)
				}
				var err error
				img, err = svgRoot(t)
				if err != nil {
					return nil, err
				}
				stack = append(stack, frame{st: applyStyle(defaultStyle(), t.Attr), m: identity})
				continue
			}
			if skipped[tag] {
				skip, depth = true, 1
				continue
			}
			if refused[tag] {
				return nil, fmt.Errorf("the SVG uses <%s>, which the icon generator does not draw; flatten it to paths and plain colours", tag)
			}
			parent := stack[len(stack)-1]
			st := applyStyle(parent.st, t.Attr)
			m := parent.m
			if tr := attr(t.Attr, "transform"); tr != "" {
				tm, err := parseTransform(tr)
				if err != nil {
					return nil, err
				}
				m = m.mul(tm)
			}
			stack = append(stack, frame{st: st, m: m})
			if st.hidden {
				continue
			}
			paths, err := elementPaths(t)
			if err != nil {
				return nil, err
			}
			if len(paths) == 0 {
				continue
			}
			for i := range paths {
				paths[i] = paths[i].transform(m)
			}
			if (st.fill.col.A == 0 || st.fill.none) && st.stroke.none {
				continue
			}
			if urlPaint(attr(t.Attr, "fill")) || urlPaint(attr(t.Attr, "stroke")) {
				return nil, errors.New("the SVG paints with a reference (url(...)), which the icon generator does not draw")
			}
			img.shapes = append(img.shapes, shape{paths: paths, st: st, width: st.strokeWidth * m.scale()})
		case xml.EndElement:
			if skip {
				depth--
				if depth == 0 {
					skip = false
				}
				continue
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if img == nil {
		return nil, errors.New("the file is not an SVG")
	}
	if len(img.shapes) == 0 {
		return nil, errors.New("the SVG draws nothing")
	}
	return img, nil
}

func urlPaint(v string) bool { return strings.Contains(v, "url(") }

func attr(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

func svgRoot(t xml.StartElement) (*svgImage, error) {
	img := &svgImage{}
	if vb := attr(t.Attr, "viewBox"); vb != "" {
		nums, err := numbers(vb)
		if err != nil || len(nums) != 4 {
			return nil, fmt.Errorf("the SVG viewBox %q is not four numbers", vb)
		}
		img.vbX, img.vbY, img.vbW, img.vbH = nums[0], nums[1], nums[2], nums[3]
	} else {
		w, h := length(attr(t.Attr, "width")), length(attr(t.Attr, "height"))
		if w <= 0 || h <= 0 {
			return nil, errors.New("the SVG has neither a viewBox nor a width and a height")
		}
		img.vbW, img.vbH = w, h
	}
	if img.vbW <= 0 || img.vbH <= 0 {
		return nil, errors.New("the SVG viewBox has no area")
	}
	img.k = svgNominal / math.Max(img.vbW, img.vbH)
	img.w = max(1, int(math.Round(img.vbW*img.k)))
	img.h = max(1, int(math.Round(img.vbH*img.k)))
	return img, nil
}

// length reads a CSS length in pixels and answers 0 for anything it cannot.
func length(v string) float64 {
	v = strings.TrimSuffix(strings.TrimSpace(v), "px")
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}

// numbers reads a list of numbers separated by spaces and commas.
func numbers(v string) ([]float64, error) {
	s := &scanner{src: v}
	var out []float64
	for {
		s.skipSeparators()
		if s.done() {
			return out, nil
		}
		n, err := s.number()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
}

// applyStyle lays an element's presentation attributes, and then its style
// attribute, over what it inherited.
func applyStyle(base style, attrs []xml.Attr) style {
	st := base
	set := func(name, value string) {
		value = strings.TrimSpace(value)
		switch name {
		case "fill":
			st.fill = parsePaint(value, st.fill)
		case "stroke":
			st.stroke = parsePaint(value, st.stroke)
		case "stroke-width":
			if f := length(value); f >= 0 && value != "" {
				st.strokeWidth = f
			}
		case "opacity":
			st.opacity = base.opacity * unit(value, 1)
		case "fill-opacity":
			st.fillOpacity = unit(value, st.fillOpacity)
		case "stroke-opacity":
			st.strokeAlpha = unit(value, st.strokeAlpha)
		case "fill-rule":
			st.evenOdd = value == "evenodd"
		case "stroke-linecap":
			st.cap = value
		case "stroke-linejoin":
			st.join = value
		case "stroke-miterlimit":
			if f := length(value); f >= 1 {
				st.miterLimit = f
			}
		case "display":
			if value == "none" {
				st.hidden = true
			}
		case "visibility":
			if value == "hidden" {
				st.hidden = true
			}
		}
	}
	for _, a := range attrs {
		if a.Name.Space != "" {
			continue
		}
		if a.Name.Local == "style" {
			continue
		}
		set(a.Name.Local, a.Value)
	}
	for _, decl := range strings.Split(attr(attrs, "style"), ";") {
		name, value, ok := strings.Cut(decl, ":")
		if ok {
			set(strings.TrimSpace(name), value)
		}
	}
	return st
}

// unit reads a number between 0 and 1, also written as a percentage.
func unit(v string, fallback float64) float64 {
	if v == "" {
		return fallback
	}
	pct := strings.HasSuffix(v, "%")
	f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
	if err != nil {
		return fallback
	}
	if pct {
		f /= 100
	}
	return math.Min(1, math.Max(0, f))
}

func parsePaint(v string, fallback paint) paint {
	switch strings.ToLower(v) {
	case "":
		return fallback
	case "none", "transparent":
		return paint{none: true}
	case "currentcolor", "inherit":
		return paint{col: color.NRGBA{A: 255}}
	}
	if strings.HasPrefix(v, "url(") {
		return fallback
	}
	if c, ok := parseColor(v); ok {
		return paint{col: c}
	}
	return fallback
}

var namedColors = map[string]color.NRGBA{
	"black": {0, 0, 0, 255}, "white": {255, 255, 255, 255}, "red": {255, 0, 0, 255},
	"green": {0, 128, 0, 255}, "blue": {0, 0, 255, 255}, "gray": {128, 128, 128, 255},
	"grey": {128, 128, 128, 255}, "yellow": {255, 255, 0, 255}, "orange": {255, 165, 0, 255},
	"silver": {192, 192, 192, 255}, "navy": {0, 0, 128, 255}, "teal": {0, 128, 128, 255},
}

// parseColor reads #rgb, #rgba, #rrggbb, #rrggbbaa, rgb(), rgba() and a few names.
func parseColor(v string) (color.NRGBA, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if c, ok := namedColors[v]; ok {
		return c, true
	}
	if rest, ok := strings.CutPrefix(v, "#"); ok {
		switch len(rest) {
		case 3, 4:
			var c [4]uint8
			c[3] = 255
			for i := 0; i < len(rest); i++ {
				n, err := strconv.ParseUint(rest[i:i+1], 16, 8)
				if err != nil {
					return color.NRGBA{}, false
				}
				c[i] = uint8(n * 17)
			}
			return color.NRGBA{c[0], c[1], c[2], c[3]}, true
		case 6, 8:
			var c [4]uint8
			c[3] = 255
			for i := 0; i < len(rest)/2; i++ {
				n, err := strconv.ParseUint(rest[2*i:2*i+2], 16, 8)
				if err != nil {
					return color.NRGBA{}, false
				}
				c[i] = uint8(n)
			}
			return color.NRGBA{c[0], c[1], c[2], c[3]}, true
		}
		return color.NRGBA{}, false
	}
	if strings.HasPrefix(v, "rgb") {
		open, end := strings.Index(v, "("), strings.LastIndex(v, ")")
		if open < 0 || end < open {
			return color.NRGBA{}, false
		}
		parts := strings.FieldsFunc(v[open+1:end], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(parts) < 3 {
			return color.NRGBA{}, false
		}
		var c [4]uint8
		c[3] = 255
		for i := 0; i < 3; i++ {
			p := parts[i]
			if rest, ok := strings.CutSuffix(p, "%"); ok {
				f, err := strconv.ParseFloat(rest, 64)
				if err != nil {
					return color.NRGBA{}, false
				}
				c[i] = uint8(math.Round(math.Min(100, math.Max(0, f)) * 2.55))
				continue
			}
			f, err := strconv.ParseFloat(p, 64)
			if err != nil {
				return color.NRGBA{}, false
			}
			c[i] = uint8(math.Min(255, math.Max(0, math.Round(f))))
		}
		if len(parts) > 3 {
			c[3] = uint8(math.Round(unit(parts[3], 1) * 255))
		}
		return color.NRGBA{c[0], c[1], c[2], c[3]}, true
	}
	return color.NRGBA{}, false
}

// parseTransform reads a transform list.
func parseTransform(v string) (matrix, error) {
	out := identity
	rest := strings.TrimSpace(v)
	for rest != "" {
		open := strings.Index(rest, "(")
		end := strings.Index(rest, ")")
		if open < 0 || end < open {
			return identity, fmt.Errorf("the SVG transform %q is malformed", v)
		}
		name := strings.TrimSpace(strings.Trim(rest[:open], ", "))
		args, err := numbers(rest[open+1 : end])
		if err != nil {
			return identity, fmt.Errorf("the SVG transform %q is malformed", v)
		}
		var m matrix
		switch {
		case name == "matrix" && len(args) == 6:
			m = matrix{args[0], args[1], args[2], args[3], args[4], args[5]}
		case name == "translate" && (len(args) == 1 || len(args) == 2):
			ty := 0.0
			if len(args) == 2 {
				ty = args[1]
			}
			m = matrix{1, 0, 0, 1, args[0], ty}
		case name == "scale" && (len(args) == 1 || len(args) == 2):
			sy := args[0]
			if len(args) == 2 {
				sy = args[1]
			}
			m = matrix{args[0], 0, 0, sy, 0, 0}
		case name == "rotate" && (len(args) == 1 || len(args) == 3):
			a := args[0] * math.Pi / 180
			c, s := math.Cos(a), math.Sin(a)
			m = matrix{c, s, -s, c, 0, 0}
			if len(args) == 3 {
				m = matrix{1, 0, 0, 1, args[1], args[2]}.mul(m).mul(matrix{1, 0, 0, 1, -args[1], -args[2]})
			}
		case name == "skewX" && len(args) == 1:
			m = matrix{1, 0, math.Tan(args[0] * math.Pi / 180), 1, 0, 0}
		case name == "skewY" && len(args) == 1:
			m = matrix{1, math.Tan(args[0] * math.Pi / 180), 0, 1, 0, 0}
		default:
			return identity, fmt.Errorf("the SVG transform %q is not one this reads", name)
		}
		out = out.mul(m)
		rest = strings.TrimLeft(rest[end+1:], ", \t\r\n")
	}
	return out, nil
}

// elementPaths is the contours an element draws, in its own coordinates.
func elementPaths(t xml.StartElement) ([]subpath, error) {
	num := func(name string) float64 { return length(attr(t.Attr, name)) }
	switch t.Name.Local {
	case "path":
		return parsePath(attr(t.Attr, "d"))
	case "rect":
		x, y, w, h := num("x"), num("y"), num("width"), num("height")
		if w <= 0 || h <= 0 {
			return nil, nil
		}
		rx, ry := num("rx"), num("ry")
		if rx == 0 {
			rx = ry
		}
		if ry == 0 {
			ry = rx
		}
		rx, ry = math.Min(rx, w/2), math.Min(ry, h/2)
		if rx <= 0 {
			return []subpath{polygon([]point{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}, true)}, nil
		}
		return []subpath{roundedRect(x, y, w, h, rx, ry)}, nil
	case "circle":
		r := num("r")
		if r <= 0 {
			return nil, nil
		}
		return []subpath{ellipse(num("cx"), num("cy"), r, r)}, nil
	case "ellipse":
		rx, ry := num("rx"), num("ry")
		if rx <= 0 || ry <= 0 {
			return nil, nil
		}
		return []subpath{ellipse(num("cx"), num("cy"), rx, ry)}, nil
	case "line":
		return []subpath{polygon([]point{{num("x1"), num("y1")}, {num("x2"), num("y2")}}, false)}, nil
	case "polyline", "polygon":
		nums, err := numbers(attr(t.Attr, "points"))
		if err != nil || len(nums) < 4 {
			return nil, fmt.Errorf("the SVG <%s> has no usable points", t.Name.Local)
		}
		var pts []point
		for i := 0; i+1 < len(nums); i += 2 {
			pts = append(pts, point{nums[i], nums[i+1]})
		}
		return []subpath{polygon(pts, t.Name.Local == "polygon")}, nil
	}
	return nil, nil
}

func polygon(pts []point, closed bool) subpath {
	s := subpath{start: pts[0], closed: closed}
	for _, p := range pts[1:] {
		s.segs = append(s.segs, segment{to: p})
	}
	return s
}

// kappa is the control-point distance that makes a cubic follow a quarter circle.
const kappa = 0.5522847498307936

func ellipse(cx, cy, rx, ry float64) subpath {
	kx, ky := rx*kappa, ry*kappa
	return subpath{
		start:  point{cx + rx, cy},
		closed: true,
		segs: []segment{
			{cubic: true, c1: point{cx + rx, cy + ky}, c2: point{cx + kx, cy + ry}, to: point{cx, cy + ry}},
			{cubic: true, c1: point{cx - kx, cy + ry}, c2: point{cx - rx, cy + ky}, to: point{cx - rx, cy}},
			{cubic: true, c1: point{cx - rx, cy - ky}, c2: point{cx - kx, cy - ry}, to: point{cx, cy - ry}},
			{cubic: true, c1: point{cx + kx, cy - ry}, c2: point{cx + rx, cy - ky}, to: point{cx + rx, cy}},
		},
	}
}

func roundedRect(x, y, w, h, rx, ry float64) subpath {
	kx, ky := rx*kappa, ry*kappa
	s := subpath{start: point{x + rx, y}, closed: true}
	line := func(p point) { s.segs = append(s.segs, segment{to: p}) }
	curve := func(a, b, c point) { s.segs = append(s.segs, segment{cubic: true, c1: a, c2: b, to: c}) }
	line(point{x + w - rx, y})
	curve(point{x + w - rx + kx, y}, point{x + w, y + ry - ky}, point{x + w, y + ry})
	line(point{x + w, y + h - ry})
	curve(point{x + w, y + h - ry + ky}, point{x + w - rx + kx, y + h}, point{x + w - rx, y + h})
	line(point{x + rx, y + h})
	curve(point{x + rx - kx, y + h}, point{x, y + h - ry + ky}, point{x, y + h - ry})
	line(point{x, y + ry})
	curve(point{x, y + ry - ky}, point{x + rx - kx, y}, point{x + rx, y})
	return s
}

// scanner reads path data and number lists.
type scanner struct {
	src string
	pos int
}

func (s *scanner) done() bool { return s.pos >= len(s.src) }

func (s *scanner) skipSeparators() {
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case ' ', ',', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

// number reads one number: a sign, digits, a point, digits, an exponent. Two
// numbers may touch ("1.5.5" is 1.5 and .5, "1-2" is 1 and -2).
func (s *scanner) number() (float64, error) {
	s.skipSeparators()
	start := s.pos
	if s.pos < len(s.src) && (s.src[s.pos] == '+' || s.src[s.pos] == '-') {
		s.pos++
	}
	digits := false
	for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
		s.pos++
		digits = true
	}
	if s.pos < len(s.src) && s.src[s.pos] == '.' {
		s.pos++
		for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
			s.pos++
			digits = true
		}
	}
	if !digits {
		return 0, fmt.Errorf("a number was expected at %q", s.rest())
	}
	if s.pos < len(s.src) && (s.src[s.pos] == 'e' || s.src[s.pos] == 'E') {
		save := s.pos
		s.pos++
		if s.pos < len(s.src) && (s.src[s.pos] == '+' || s.src[s.pos] == '-') {
			s.pos++
		}
		exp := false
		for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
			s.pos++
			exp = true
		}
		if !exp {
			s.pos = save
		}
	}
	return strconv.ParseFloat(s.src[start:s.pos], 64)
}

// flag reads an arc flag, which is one digit and may touch what follows.
func (s *scanner) flag() (bool, error) {
	s.skipSeparators()
	if s.pos < len(s.src) && (s.src[s.pos] == '0' || s.src[s.pos] == '1') {
		s.pos++
		return s.src[s.pos-1] == '1', nil
	}
	return false, fmt.Errorf("an arc flag was expected at %q", s.rest())
}

func (s *scanner) rest() string {
	end := min(len(s.src), s.pos+12)
	return s.src[s.pos:end]
}

// parsePath reads path data into contours.
func parsePath(d string) ([]subpath, error) {
	s := &scanner{src: d}
	var (
		paths   []subpath
		cur     *subpath
		pos     point
		start   point
		prevC   point // the last cubic control point, for S
		prevQ   point // the last quadratic control point, for T
		lastCmd byte
		cmd     byte
	)
	begin := func(p point) {
		paths = append(paths, subpath{start: p})
		cur = &paths[len(paths)-1]
		start = p
	}
	// add appends to the open contour, opening one at the current point when
	// the data goes on drawing after a close.
	add := func(g segment) {
		if cur == nil {
			begin(pos)
		}
		cur.segs = append(cur.segs, g)
		pos = g.to
	}
	for {
		s.skipSeparators()
		if s.done() {
			break
		}
		c := s.src[s.pos]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			cmd = c
			s.pos++
			if cmd == 'z' || cmd == 'Z' {
				if cur != nil {
					cur.closed = true
					pos = start
					cur = nil
				}
				lastCmd = cmd
				continue
			}
		} else if cmd == 0 || cmd == 'z' || cmd == 'Z' {
			return nil, fmt.Errorf("the path data has a number where a command was expected, at %q", s.rest())
		} else if cmd == 'M' {
			cmd = 'L'
		} else if cmd == 'm' {
			cmd = 'l'
		}
		rel := cmd >= 'a'
		up := cmd &^ 0x20
		at := func(x, y float64) point {
			if rel {
				return point{pos.x + x, pos.y + y}
			}
			return point{x, y}
		}
		read := func(n int) ([]float64, error) {
			out := make([]float64, n)
			for i := range out {
				v, err := s.number()
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
		switch up {
		case 'M':
			v, err := read(2)
			if err != nil {
				return nil, err
			}
			p := at(v[0], v[1])
			pos = p
			begin(p)
		case 'L':
			v, err := read(2)
			if err != nil {
				return nil, err
			}
			add(segment{to: at(v[0], v[1])})
		case 'H':
			v, err := read(1)
			if err != nil {
				return nil, err
			}
			x := v[0]
			if rel {
				x += pos.x
			}
			add(segment{to: point{x, pos.y}})
		case 'V':
			v, err := read(1)
			if err != nil {
				return nil, err
			}
			y := v[0]
			if rel {
				y += pos.y
			}
			add(segment{to: point{pos.x, y}})
		case 'C':
			v, err := read(6)
			if err != nil {
				return nil, err
			}
			g := segment{cubic: true, c1: at(v[0], v[1]), c2: at(v[2], v[3]), to: at(v[4], v[5])}
			prevC = g.c2
			add(g)
		case 'S':
			v, err := read(4)
			if err != nil {
				return nil, err
			}
			c1 := pos
			if lastCmd == 'C' || lastCmd == 'S' {
				c1 = point{2*pos.x - prevC.x, 2*pos.y - prevC.y}
			}
			g := segment{cubic: true, c1: c1, c2: at(v[0], v[1]), to: at(v[2], v[3])}
			prevC = g.c2
			add(g)
		case 'Q':
			v, err := read(4)
			if err != nil {
				return nil, err
			}
			q := at(v[0], v[1])
			prevQ = q
			add(quadratic(pos, q, at(v[2], v[3])))
		case 'T':
			v, err := read(2)
			if err != nil {
				return nil, err
			}
			q := pos
			if lastCmd == 'Q' || lastCmd == 'T' {
				q = point{2*pos.x - prevQ.x, 2*pos.y - prevQ.y}
			}
			prevQ = q
			add(quadratic(pos, q, at(v[0], v[1])))
		case 'A':
			v, err := read(3)
			if err != nil {
				return nil, err
			}
			large, err := s.flag()
			if err != nil {
				return nil, err
			}
			sweep, err := s.flag()
			if err != nil {
				return nil, err
			}
			e, err := read(2)
			if err != nil {
				return nil, err
			}
			to := at(e[0], e[1])
			from := pos
			for _, g := range arc(from, v[0], v[1], v[2], large, sweep, to) {
				add(g)
			}
		default:
			return nil, fmt.Errorf("the path data has the command %q, which is not one", string(cmd))
		}
		lastCmd = up
	}
	var out []subpath
	for _, p := range paths {
		if len(p.segs) > 0 {
			out = append(out, p)
		}
	}
	return out, nil
}

func quadratic(p0, q, p1 point) segment {
	return segment{
		cubic: true,
		c1:    point{p0.x + 2.0/3*(q.x-p0.x), p0.y + 2.0/3*(q.y-p0.y)},
		c2:    point{p1.x + 2.0/3*(q.x-p1.x), p1.y + 2.0/3*(q.y-p1.y)},
		to:    p1,
	}
}

// arc turns an SVG elliptical arc into cubic curves of at most a quarter turn.
func arc(from point, rx, ry, rotation float64, large, sweep bool, to point) []segment {
	if from == to {
		return nil
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		return []segment{{to: to}}
	}
	phi := rotation * math.Pi / 180
	cosPhi, sinPhi := math.Cos(phi), math.Sin(phi)
	dx, dy := (from.x-to.x)/2, (from.y-to.y)/2
	x1 := cosPhi*dx + sinPhi*dy
	y1 := -sinPhi*dx + cosPhi*dy
	if lambda := x1*x1/(rx*rx) + y1*y1/(ry*ry); lambda > 1 {
		rx *= math.Sqrt(lambda)
		ry *= math.Sqrt(lambda)
	}
	num := rx*rx*ry*ry - rx*rx*y1*y1 - ry*ry*x1*x1
	den := rx*rx*y1*y1 + ry*ry*x1*x1
	coef := 0.0
	if den != 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1 / ry
	cyp := -coef * ry * x1 / rx
	cx := cosPhi*cxp - sinPhi*cyp + (from.x+to.x)/2
	cy := sinPhi*cxp + cosPhi*cyp + (from.y+to.y)/2
	angle := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	theta := angle(1, 0, (x1-cxp)/rx, (y1-cyp)/ry)
	delta := angle((x1-cxp)/rx, (y1-cyp)/ry, (-x1-cxp)/rx, (-y1-cyp)/ry)
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	} else if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(delta) / (math.Pi / 2)))
	n = max(n, 1)
	step := delta / float64(n)
	t := 4.0 / 3 * math.Tan(step/4)
	at := func(a float64) (point, point) {
		ca, sa := math.Cos(a), math.Sin(a)
		p := point{cx + rx*ca*cosPhi - ry*sa*sinPhi, cy + rx*ca*sinPhi + ry*sa*cosPhi}
		d := point{-rx*sa*cosPhi - ry*ca*sinPhi, -rx*sa*sinPhi + ry*ca*cosPhi}
		return p, d
	}
	var out []segment
	for i := 0; i < n; i++ {
		a0, a1 := theta+float64(i)*step, theta+float64(i+1)*step
		p0, d0 := at(a0)
		p1, d1 := at(a1)
		g := segment{
			cubic: true,
			c1:    point{p0.x + t*d0.x, p0.y + t*d0.y},
			c2:    point{p1.x - t*d1.x, p1.y - t*d1.y},
			to:    p1,
		}
		if i == n-1 {
			g.to = to
		}
		out = append(out, g)
	}
	return out
}

// Bounds is the drawing's natural size.
func (s *svgImage) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }

// Draw rasterises the part of the drawing in src into r.
func (s *svgImage) Draw(dst *image.NRGBA, r, src image.Rectangle) {
	if r.Empty() || src.Empty() {
		return
	}
	sx := float64(r.Dx()) / float64(src.Dx())
	sy := float64(r.Dy()) / float64(src.Dy())
	// device maps the drawing's coordinates into r's own pixels.
	device := matrix{
		s.k * sx, 0, 0, s.k * sy,
		(-s.vbX*s.k - float64(src.Min.X)) * sx,
		(-s.vbY*s.k - float64(src.Min.Y)) * sy,
	}
	// scale is how much longer a length of the drawing is on the canvas.
	scale := s.k * math.Sqrt(math.Abs(sx*sy))
	canvas := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	ras := vector.NewRasterizer(r.Dx(), r.Dy())
	for _, sh := range s.shapes {
		if !sh.st.fill.none && sh.st.fill.col.A > 0 {
			paths := make([]subpath, len(sh.paths))
			for i, p := range sh.paths {
				paths[i] = p.transform(device)
			}
			if sh.st.evenOdd {
				paths = orientEvenOdd(paths)
			}
			ras.Reset(r.Dx(), r.Dy())
			for _, p := range paths {
				addPath(ras, p)
			}
			col := sh.st.fill.col
			col.A = uint8(math.Round(float64(col.A) * sh.st.opacity * sh.st.fillOpacity))
			ras.Draw(canvas, canvas.Bounds(), image.NewUniform(col), image.Point{})
		}
		if !sh.st.stroke.none && sh.st.stroke.col.A > 0 && sh.width > 0 {
			ras.Reset(r.Dx(), r.Dy())
			w := sh.width * scale
			for _, p := range sh.paths {
				d := p.transform(device)
				for _, poly := range stroke(d.polyline(3), d.closed, w, sh.st.cap, sh.st.join, sh.st.miterLimit) {
					addPolygon(ras, poly)
				}
			}
			col := sh.st.stroke.col
			col.A = uint8(math.Round(float64(col.A) * sh.st.opacity * sh.st.strokeAlpha))
			ras.Draw(canvas, canvas.Bounds(), image.NewUniform(col), image.Point{})
		}
	}
	draw.Draw(dst, r, canvas, image.Point{}, draw.Over)
}

func addPath(ras *vector.Rasterizer, p subpath) {
	ras.MoveTo(float32(p.start.x), float32(p.start.y))
	for _, g := range p.segs {
		if g.cubic {
			ras.CubeTo(float32(g.c1.x), float32(g.c1.y), float32(g.c2.x), float32(g.c2.y), float32(g.to.x), float32(g.to.y))
		} else {
			ras.LineTo(float32(g.to.x), float32(g.to.y))
		}
	}
	ras.ClosePath()
}

func addPolygon(ras *vector.Rasterizer, poly []point) {
	if len(poly) < 3 {
		return
	}
	ras.MoveTo(float32(poly[0].x), float32(poly[0].y))
	for _, p := range poly[1:] {
		ras.LineTo(float32(p.x), float32(p.y))
	}
	ras.ClosePath()
}

// area is the signed area of a polygon; positive is the direction the
// rasteriser counts as filled.
func area(poly []point) float64 {
	a := 0.0
	for i := range poly {
		j := (i + 1) % len(poly)
		a += poly[i].x*poly[j].y - poly[j].x*poly[i].y
	}
	return a / 2
}

// orientEvenOdd turns the even-odd rule into what the rasteriser counts, which
// is a winding number: each contour is wound the way its depth says, so a
// contour inside one other is a hole and a contour inside that one is filled
// again.
func orientEvenOdd(paths []subpath) []subpath {
	polys := make([][]point, len(paths))
	for i, p := range paths {
		polys[i] = p.polyline(4)
	}
	out := make([]subpath, len(paths))
	for i, p := range paths {
		depth := 0
		for j := range paths {
			if i != j && inside(polys[j], polys[i][0]) && !onBoundary(polys[j], polys[i][0]) {
				depth++
			}
		}
		wantPositive := depth%2 == 0
		if (area(polys[i]) > 0) != wantPositive {
			out[i] = p.reversed()
		} else {
			out[i] = p
		}
	}
	return out
}

func onBoundary(poly []point, p point) bool {
	for i := range poly {
		j := (i + 1) % len(poly)
		if dist(poly[i], p)+dist(p, poly[j])-dist(poly[i], poly[j]) < 1e-9 {
			return true
		}
	}
	return false
}

// inside is the crossing-number test.
func inside(poly []point, p point) bool {
	in := false
	for i := range poly {
		j := (i + 1) % len(poly)
		a, b := poly[i], poly[j]
		if (a.y > p.y) != (b.y > p.y) && p.x < (b.x-a.x)*(p.y-a.y)/(b.y-a.y)+a.x {
			in = !in
		}
	}
	return in
}

// stroke turns a polyline into the polygons whose union is its stroke. Every
// polygon is wound the same way, so the rasteriser's winding number adds them
// up and clamps at one.
func stroke(pts []point, closed bool, width float64, capStyle, join string, miterLimit float64) [][]point {
	// Drop repeated points: they have no direction.
	clean := pts[:0:0]
	for _, p := range pts {
		if len(clean) == 0 || dist(clean[len(clean)-1], p) > 1e-6 {
			clean = append(clean, p)
		}
	}
	if closed && len(clean) > 1 && dist(clean[0], clean[len(clean)-1]) < 1e-6 {
		clean = clean[:len(clean)-1]
	}
	if len(clean) < 2 {
		if len(clean) == 1 && capStyle == "round" {
			return [][]point{circle(clean[0], width/2)}
		}
		return nil
	}
	half := width / 2
	var out [][]point
	emit := func(poly []point) {
		if area(poly) < 0 {
			for i, j := 0, len(poly)-1; i < j; i, j = i+1, j-1 {
				poly[i], poly[j] = poly[j], poly[i]
			}
		}
		out = append(out, poly)
	}
	n := len(clean)
	segs := n - 1
	if closed {
		segs = n
	}
	for i := 0; i < segs; i++ {
		a, b := clean[i], clean[(i+1)%n]
		dx, dy := b.x-a.x, b.y-a.y
		l := math.Hypot(dx, dy)
		nx, ny := -dy/l*half, dx/l*half
		ea, eb := a, b
		if !closed && capStyle == "square" {
			if i == 0 {
				ea = point{a.x - dx/l*half, a.y - dy/l*half}
			}
			if i == segs-1 {
				eb = point{b.x + dx/l*half, b.y + dy/l*half}
			}
		}
		emit([]point{{ea.x + nx, ea.y + ny}, {eb.x + nx, eb.y + ny}, {eb.x - nx, eb.y - ny}, {ea.x - nx, ea.y - ny}})
	}
	// Joins sit at every vertex that has a segment on both sides.
	for i := 0; i < n; i++ {
		if !closed && (i == 0 || i == n-1) {
			continue
		}
		prev, v, next := clean[(i+n-1)%n], clean[i], clean[(i+1)%n]
		if join == "round" {
			emit(circle(v, half))
			continue
		}
		d1 := unitVec(prev, v)
		d2 := unitVec(v, next)
		cross := d1.x*d2.y - d1.y*d2.x
		if math.Abs(cross) < 1e-9 && d1.x*d2.x+d1.y*d2.y > 0 {
			continue // straight on
		}
		// The outer side is opposite the turn.
		side := -1.0
		if cross < 0 {
			side = 1.0
		}
		o1 := point{v.x + side*(-d1.y)*half, v.y + side*d1.x*half}
		o2 := point{v.x + side*(-d2.y)*half, v.y + side*d2.x*half}
		bevel := []point{v, o1, o2}
		if join == "miter" || join == "miter-clip" || join == "" {
			// The miter's length over the stroke's width is 1/sin(theta/2), theta the
			// angle between the segments.
			cosT := -(d1.x*d2.x + d1.y*d2.y)
			sinHalf := math.Sqrt(math.Max(0, (1-cosT)/2))
			if sinHalf > 1e-6 && 1/sinHalf <= miterLimit {
				mx, my := (o1.x+o2.x)/2-v.x, (o1.y+o2.y)/2-v.y
				ml := math.Hypot(mx, my)
				if ml > 1e-9 {
					tip := half / sinHalf
					bevel = []point{v, o1, {v.x + mx/ml*tip, v.y + my/ml*tip}, o2}
				}
			}
		}
		emit(bevel)
	}
	if !closed && capStyle == "round" {
		emit(circle(clean[0], half))
		emit(circle(clean[n-1], half))
	}
	return out
}

func unitVec(a, b point) point {
	l := dist(a, b)
	return point{(b.x - a.x) / l, (b.y - a.y) / l}
}

func circle(c point, r float64) []point {
	n := max(12, min(64, int(math.Ceil(r*2))))
	pts := make([]point, n)
	for i := range pts {
		a := 2 * math.Pi * float64(i) / float64(n)
		pts[i] = point{c.x + r*math.Cos(a), c.y + r*math.Sin(a)}
	}
	return pts
}
