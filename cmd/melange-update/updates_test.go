package main

import "testing"

func TestShortName(t *testing.T) {
	for _, tt := range []struct {
		name, want string
	}{
		{"caddy", "caddy"},
		{"github.com/pdeslaur/caddy-pocketid", "caddy-pocketid"},
		{"github.com/caddy-dns/acmeproxy", "acmeproxy"},
		{"github.com/foo/bar/v2", "bar"},
		{"github.com/foo/vault", "vault"},
	} {
		if got := (update{Name: tt.name}).shortName(); got != tt.want {
			t.Errorf("shortName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestTitleAndBody(t *testing.T) {
	updates := []update{{
		Name: "caddy",
		From: "v2.11.7",
		To:   "v2.12.0",
		URL:  "https://github.com/caddyserver/caddy/releases/tag/v2.12.0",
	}, {
		Name: "github.com/pdeslaur/caddy-pocketid",
		From: "v0.1.5",
		To:   "v0.1.6",
		URL:  "https://pkg.go.dev/github.com/pdeslaur/caddy-pocketid@v0.1.6",
	}}

	if got, want := title(updates), "Update caddy to v2.12.0, caddy-pocketid to v0.1.6"; got != want {
		t.Errorf("title() = %q, want %q", got, want)
	}

	want := `Automated version bumps for caddy:

- caddy: v2.11.7 → [v2.12.0](https://github.com/caddyserver/caddy/releases/tag/v2.12.0)
- github.com/pdeslaur/caddy-pocketid: v0.1.5 → [v0.1.6](https://pkg.go.dev/github.com/pdeslaur/caddy-pocketid@v0.1.6)
`
	if got := body("caddy", updates); got != want {
		t.Errorf("body() =\n%s\nwant:\n%s", got, want)
	}
}
