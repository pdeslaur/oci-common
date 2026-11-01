package main

import (
	"fmt"
	"path"
	"strings"
)

// update describes a single version bump applied to a melange config.
type update struct {
	// Name identifies what was bumped: the package name or a Go module path.
	Name string
	From string
	To   string
	// URL links to the release notes or documentation for To.
	URL string
}

// shortName returns the last path element of a Go module path, e.g.
// "caddy-pocketid" for "github.com/pdeslaur/caddy-pocketid", ignoring any
// major version suffix.
func (u update) shortName() string {
	base := path.Base(u.Name)
	if len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		base = path.Base(path.Dir(u.Name))
	}
	return base
}

// title summarizes updates for a pull request title and commit message.
func title(updates []update) string {
	parts := make([]string, len(updates))
	for i, u := range updates {
		parts[i] = u.shortName() + " to " + u.To
	}
	return "Update " + strings.Join(parts, ", ")
}

// body describes updates for a pull request body as a Markdown list.
func body(pkg string, updates []update) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Automated version bumps for %s:\n\n", pkg)
	for _, u := range updates {
		fmt.Fprintf(&b, "- %s: %s → [%s](%s)\n", u.Name, u.From, u.To, u.URL)
	}
	return b.String()
}
