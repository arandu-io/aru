package main

import (
	"net/url"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/doctor"
)

// namedRoute is one route a page can be visited by name: a GET route the
// project's route files register with a name.
type namedRoute struct {
	Name    string
	Pattern string
}

// projectRoutes reads the named GET routes of the project at root from its
// source, the way the project map reads them. A project whose map cannot be
// built has no names to offer, and every address is then written as a path.
func projectRoutes(root string) []namedRoute {
	profile, err := doctor.DeclaredProfile(root)
	if err != nil {
		profile = doctor.Conventional
	}
	analysis, err := doctor.AnalyzeWithMap(root, profile)
	if err != nil {
		return nil
	}
	var out []namedRoute
	for _, n := range analysis.Map.Nodes {
		if n.Kind != "route" || n.Name == "" || (n.Method != "GET" && n.Method != "ANY") {
			continue
		}
		out = append(out, namedRoute{Name: n.Name, Pattern: n.Pattern})
	}
	// The route with more literal segments wins a tie, so /invoices/new is
	// invoices.create and not invoices.show with "new" for its id.
	sort.SliceStable(out, func(i, j int) bool { return literalSegments(out[i].Pattern) > literalSegments(out[j].Pattern) })
	return out
}

func literalSegments(pattern string) int {
	n := 0
	for _, seg := range strings.Split(strings.Trim(pattern, "/"), "/") {
		if !strings.HasPrefix(seg, "{") {
			n++
		}
	}
	return n
}

// matchRoute answers the route whose pattern the path fills, and the values of
// its parameters in order.
func matchRoute(routes []namedRoute, path string) (string, []string, bool) {
	for _, r := range routes {
		if params, ok := matchPattern(r.Pattern, path); ok {
			return r.Name, params, true
		}
	}
	return "", nil, false
}

// matchPattern fills one pattern with a path, segment by segment: a literal
// segment must be equal, a {name} segment takes one non-empty segment, and a
// {name...} segment takes the rest of the path.
func matchPattern(pattern, path string) ([]string, bool) {
	want := strings.Split(strings.Trim(pattern, "/"), "/")
	have := strings.Split(strings.Trim(path, "/"), "/")
	var params []string
	for i, seg := range want {
		wildcard := strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
		if wildcard && strings.HasSuffix(seg, "...}") {
			if i >= len(have) {
				return nil, false
			}
			return append(params, strings.Join(have[i:], "/")), true
		}
		if i >= len(have) {
			return nil, false
		}
		if !wildcard {
			if seg != have[i] {
				return nil, false
			}
			continue
		}
		if have[i] == "" {
			return nil, false
		}
		value, err := url.PathUnescape(have[i])
		if err != nil {
			value = have[i]
		}
		params = append(params, value)
	}
	return params, len(want) == len(have)
}

// routeTarget is how a page step names an address: a route name and its
// parameters when a route matches the path, the path otherwise -- and the
// whole URL when it is another origin than base.
func routeTarget(routes []namedRoute, base *url.URL, raw string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return []string{goString(raw)}
	}
	if base != nil && (u.Scheme != base.Scheme || u.Host != base.Host) {
		return []string{goString(raw)}
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery == "" && u.Fragment == "" {
		if name, params, ok := matchRoute(routes, u.Path); ok {
			out := []string{goString(name)}
			for _, p := range params {
				out = append(out, goString(p))
			}
			return out
		}
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return []string{goString(path)}
}
