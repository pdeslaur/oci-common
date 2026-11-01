package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseSource(t *testing.T) {
	for _, tt := range []struct {
		file string
		want source
	}{{
		file: "testdata/caddy.yaml",
		want: source{
			Name:           "caddy",
			Version:        "2.11.7",
			Owner:          "caddyserver",
			Repo:           "caddy",
			TagTemplate:    "v${{package.version}}",
			ExpectedCommit: "72dd0fb067f6d7826c7f79907670ba4a713bfe37",
		},
	}, {
		file: "testdata/anki-sync-server.yaml",
		want: source{
			Name:           "anki-sync-server",
			Version:        "26.09.3",
			Owner:          "ankitects",
			Repo:           "anki",
			TagTemplate:    "${{package.version}}",
			ExpectedCommit: "29bb700b951e3f0c0cb69b77c0180fc1fe33e6ba",
		},
	}, {
		file: "testdata/dns01proxy.yaml",
		want: source{
			Name:           "dns01proxy",
			Version:        "0.1.20",
			Owner:          "liujed",
			Repo:           "dns01proxy",
			TagTemplate:    "v${{package.version}}",
			ExpectedCommit: "335a27ef7083277a7c54065223183ec6f538296c",
		},
	}} {
		t.Run(tt.file, func(t *testing.T) {
			got, err := parseSource(readFile(t, tt.file))
			if err != nil {
				t.Fatalf("parseSource() = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseSource() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseSourceErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		yaml string
	}{{
		name: "no git-checkout",
		yaml: `
package:
  name: foo
  version: "1.0"
pipeline:
  - runs: true
`,
	}, {
		name: "two git-checkouts",
		yaml: `
package:
  name: foo
  version: "1.0"
pipeline:
  - uses: git-checkout
    with:
      repository: https://github.com/a/b
      tag: v${{package.version}}
      expected-commit: abc
  - uses: git-checkout
    with:
      repository: https://github.com/c/d
      tag: v${{package.version}}
      expected-commit: def
`,
	}, {
		name: "not GitHub",
		yaml: `
package:
  name: foo
  version: "1.0"
pipeline:
  - uses: git-checkout
    with:
      repository: https://gitlab.com/a/b
      tag: v${{package.version}}
      expected-commit: abc
`,
	}, {
		name: "fixed tag",
		yaml: `
package:
  name: foo
  version: "1.0"
pipeline:
  - uses: git-checkout
    with:
      repository: https://github.com/a/b
      tag: v1.0
      expected-commit: abc
`,
	}, {
		name: "no expected-commit",
		yaml: `
package:
  name: foo
  version: "1.0"
pipeline:
  - uses: git-checkout
    with:
      repository: https://github.com/a/b
      tag: v${{package.version}}
`,
	}} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseSource([]byte(tt.yaml)); err == nil {
				t.Error("parseSource() succeeded, want error")
			}
		})
	}
}

func TestParseGitHubRepository(t *testing.T) {
	for _, tt := range []struct {
		url, owner, repo string
	}{
		{"https://github.com/caddyserver/caddy", "caddyserver", "caddy"},
		{"https://github.com/caddyserver/caddy.git", "caddyserver", "caddy"},
		{"https://github.com/caddyserver/caddy/", "caddyserver", "caddy"},
	} {
		owner, repo, err := parseGitHubRepository(tt.url)
		if err != nil {
			t.Errorf("parseGitHubRepository(%q) = %v", tt.url, err)
			continue
		}
		if owner != tt.owner || repo != tt.repo {
			t.Errorf("parseGitHubRepository(%q) = %q, %q, want %q, %q", tt.url, owner, repo, tt.owner, tt.repo)
		}
	}
}

func TestVersionFromTag(t *testing.T) {
	for _, tt := range []struct {
		template, tag, want string
		ok                  bool
	}{
		{"v${{package.version}}", "v2.11.7", "2.11.7", true},
		{"${{package.version}}", "26.09.3", "26.09.3", true},
		{"release-${{package.version}}-final", "release-1.2-final", "1.2", true},
		{"v${{package.version}}", "2.11.7", "", false},
		{"v${{package.version}}", "v", "", false},
	} {
		got, ok := source{TagTemplate: tt.template}.VersionFromTag(tt.tag)
		if got != tt.want || ok != tt.ok {
			t.Errorf("VersionFromTag(%q, %q) = %q, %v, want %q, %v", tt.template, tt.tag, got, ok, tt.want, tt.ok)
		}
	}
}

func TestPatchConfig(t *testing.T) {
	data := readFile(t, "testdata/caddy.yaml")
	src, err := parseSource(data)
	if err != nil {
		t.Fatalf("parseSource() = %v", err)
	}

	const commit = "0123456789abcdef0123456789abcdef01234567"
	got, err := patchConfig(data, src, "2.12.0", commit)
	if err != nil {
		t.Fatalf("patchConfig() = %v", err)
	}

	want := strings.NewReplacer(
		`    version: "2.11.7"`, `    version: "2.12.0"`,
		"          expected-commit: "+src.ExpectedCommit, "          expected-commit: "+commit,
	).Replace(string(data))
	if string(got) != want {
		t.Errorf("patchConfig() =\n%s\nwant:\n%s", got, want)
	}

	patched, err := parseSource(got)
	if err != nil {
		t.Fatalf("parseSource(patched) = %v", err)
	}
	if patched.Version != "2.12.0" || patched.ExpectedCommit != commit {
		t.Errorf("patched source = %+v", patched)
	}
}

func TestPatchConfigUnquotedVersion(t *testing.T) {
	data := []byte(`package:
  name: foo
  version: 1.0
  epoch: 3
pipeline:
  - uses: git-checkout
    with:
      repository: https://github.com/a/b
      tag: v${{package.version}}
      expected-commit: abc
`)
	src, err := parseSource(data)
	if err != nil {
		t.Fatalf("parseSource() = %v", err)
	}
	if src.Epoch != 3 {
		t.Errorf("parseSource().Epoch = %d, want 3", src.Epoch)
	}
	got, err := patchConfig(data, src, "1.1", "def")
	if err != nil {
		t.Fatalf("patchConfig() = %v", err)
	}
	want := `package:
  name: foo
  version: "1.1"
  epoch: 3
pipeline:
  - uses: git-checkout
    with:
      repository: https://github.com/a/b
      tag: v${{package.version}}
      expected-commit: def
`
	if string(got) != want {
		t.Errorf("patchConfig() =\n%s\nwant:\n%s", got, want)
	}
}

func TestSetEpoch(t *testing.T) {
	data := readFile(t, "testdata/caddy.yaml")
	got, err := setEpoch(data, 2)
	if err != nil {
		t.Fatalf("setEpoch() = %v", err)
	}
	want := strings.Replace(string(data), "    epoch: 0\n", "    epoch: 2\n", 1)
	if string(got) != want {
		t.Errorf("setEpoch() =\n%s\nwant:\n%s", got, want)
	}

	src, err := parseSource(got)
	if err != nil {
		t.Fatalf("parseSource(patched) = %v", err)
	}
	if src.Epoch != 2 {
		t.Errorf("patched epoch = %d, want 2", src.Epoch)
	}
}

func TestParseGoModules(t *testing.T) {
	for _, tt := range []struct {
		file string
		want []goModule
	}{{
		file: "testdata/caddy.yaml",
		want: []goModule{
			{Path: "github.com/caddy-dns/acmeproxy", Version: "v1.1.1"},
			{Path: "github.com/pdeslaur/caddy-pocketid", Version: "v0.1.5"},
		},
	}, {
		file: "testdata/dns01proxy.yaml",
	}, {
		file: "testdata/anki-sync-server.yaml",
	}} {
		t.Run(tt.file, func(t *testing.T) {
			got := parseGoModules(readFile(t, tt.file))
			if !slices.Equal(got, tt.want) {
				t.Errorf("parseGoModules() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPatchGoModule(t *testing.T) {
	data := readFile(t, "testdata/caddy.yaml")
	m := goModule{Path: "github.com/pdeslaur/caddy-pocketid", Version: "v0.1.5"}

	got, err := patchGoModule(data, m, "v0.2.0")
	if err != nil {
		t.Fatalf("patchGoModule() = %v", err)
	}
	want := strings.Replace(string(data),
		"go get github.com/pdeslaur/caddy-pocketid@v0.1.5\n",
		"go get github.com/pdeslaur/caddy-pocketid@v0.2.0\n", 1)
	if string(got) != want {
		t.Errorf("patchGoModule() =\n%s\nwant:\n%s", got, want)
	}

	if _, err := patchGoModule(data, goModule{Path: "github.com/a/b", Version: "v1.0.0"}, "v1.1.0"); err == nil {
		t.Error("patchGoModule() of unpinned module succeeded, want error")
	}
}

func TestPatchConfigAmbiguous(t *testing.T) {
	data := []byte(`package:
  name: foo
  version: "1.0"
  epoch: 0
pipeline:
  - uses: git-checkout
    with:
      repository: https://github.com/a/b
      tag: v${{package.version}}
      expected-commit: abc
  - uses: fetch
    with:
      version: "1.0"
`)
	src, err := parseSource(data)
	if err != nil {
		t.Fatalf("parseSource() = %v", err)
	}
	if _, err := patchConfig(data, src, "1.1", "def"); err == nil {
		t.Error("patchConfig() succeeded, want error")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"2.11.7", "2.11.7", 0},
		{"2.11.7", "2.11.10", -1},
		{"2.12.0", "2.11.10", 1},
		{"26.09", "26.09.3", -1},
		{"26.09", "26.09.0", 0},
		{"26.10", "26.09.3", 1},
		{"0.1.9", "0.1.20", -1},
	} {
		got, err := compareVersions(tt.a, tt.b)
		if err != nil {
			t.Errorf("compareVersions(%q, %q) = %v", tt.a, tt.b, err)
			continue
		}
		if got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCompareVersionsErrors(t *testing.T) {
	for _, tt := range [][2]string{
		{"2.11.7-rc1", "2.11.7"},
		{"2.11.7", "2..7"},
		{"", "1"},
	} {
		if _, err := compareVersions(tt[0], tt[1]); err == nil {
			t.Errorf("compareVersions(%q, %q) succeeded, want error", tt[0], tt[1])
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
