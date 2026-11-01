package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const versionPlaceholder = "${{package.version}}"

// source describes a melange package that is built from a single upstream
// GitHub repository using the git-checkout pipeline.
type source struct {
	Name    string
	Version string
	Epoch   int
	// Owner and Repo identify the upstream GitHub repository.
	Owner string
	Repo  string
	// TagTemplate is the git-checkout tag, e.g. "v${{package.version}}".
	TagTemplate    string
	ExpectedCommit string
}

// Tag returns the upstream git tag for the given version.
func (s source) Tag(version string) string {
	return strings.ReplaceAll(s.TagTemplate, versionPlaceholder, version)
}

// VersionFromTag extracts the version from an upstream git tag, or reports
// false if the tag doesn't match the tag template.
func (s source) VersionFromTag(tag string) (string, bool) {
	prefix, suffix, _ := strings.Cut(s.TagTemplate, versionPlaceholder)
	if !strings.HasPrefix(tag, prefix) || !strings.HasSuffix(tag, suffix) {
		return "", false
	}
	v := strings.TrimSuffix(strings.TrimPrefix(tag, prefix), suffix)
	return v, v != ""
}

type melangeConfig struct {
	Package struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
		Epoch   int    `yaml:"epoch"`
	} `yaml:"package"`
	Pipeline []struct {
		Uses string `yaml:"uses"`
		With struct {
			Repository     string `yaml:"repository"`
			Tag            string `yaml:"tag"`
			ExpectedCommit string `yaml:"expected-commit"`
		} `yaml:"with"`
	} `yaml:"pipeline"`
}

// parseSource extracts the package's upstream source from a melange config.
func parseSource(data []byte) (source, error) {
	var cfg melangeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return source{}, fmt.Errorf("parsing melange config: %w", err)
	}

	s := source{Name: cfg.Package.Name, Version: cfg.Package.Version, Epoch: cfg.Package.Epoch}
	if s.Name == "" || s.Version == "" {
		return source{}, fmt.Errorf("package.name and package.version are required")
	}

	found := 0
	for _, step := range cfg.Pipeline {
		if step.Uses != "git-checkout" {
			continue
		}
		found++
		owner, repo, err := parseGitHubRepository(step.With.Repository)
		if err != nil {
			return source{}, err
		}
		s.Owner, s.Repo = owner, repo
		s.TagTemplate = step.With.Tag
		s.ExpectedCommit = step.With.ExpectedCommit
	}
	if found != 1 {
		return source{}, fmt.Errorf("expected exactly one git-checkout step, found %d", found)
	}
	if !strings.Contains(s.TagTemplate, versionPlaceholder) {
		return source{}, fmt.Errorf("git-checkout tag %q does not reference %s", s.TagTemplate, versionPlaceholder)
	}
	if s.ExpectedCommit == "" {
		return source{}, fmt.Errorf("git-checkout expected-commit is required")
	}
	return s, nil
}

func parseGitHubRepository(u string) (owner, repo string, err error) {
	path, ok := strings.CutPrefix(u, "https://github.com/")
	if !ok {
		return "", "", fmt.Errorf("repository %q is not a GitHub URL", u)
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	owner, repo, ok = strings.Cut(path, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("repository %q is not of the form https://github.com/OWNER/REPO", u)
	}
	return owner, repo, nil
}

// patchConfig rewrites the package version and expected commit in a melange
// config. Like the other patch functions, it edits the text in place rather
// than re-marshalling so that formatting and comments are preserved.
func patchConfig(data []byte, s source, version, commit string) ([]byte, error) {
	out, err := replaceOnce(data, `version:\s*"?`+regexp.QuoteMeta(s.Version)+`"?`, `version: "`+version+`"`)
	if err != nil {
		return nil, fmt.Errorf("patching version: %w", err)
	}
	out, err = replaceOnce(out, `expected-commit:\s*`+regexp.QuoteMeta(s.ExpectedCommit), "expected-commit: "+commit)
	if err != nil {
		return nil, fmt.Errorf("patching expected-commit: %w", err)
	}
	return out, nil
}

// setEpoch rewrites the package epoch in a melange config.
func setEpoch(data []byte, epoch int) ([]byte, error) {
	out, err := replaceOnce(data, `epoch:\s*\d+`, "epoch: "+strconv.Itoa(epoch))
	if err != nil {
		return nil, fmt.Errorf("patching epoch: %w", err)
	}
	return out, nil
}

// goModule is a Go module pinned in a pipeline step with
// "go get MODULE@VERSION", e.g. to add a plugin to the upstream source.
type goModule struct {
	Path    string
	Version string
}

var goGetRe = regexp.MustCompile(`(?m)^[ \t]+go get ([^\s@]+)@(v[^\s]+)[ \t]*$`)

// parseGoModules returns the Go modules pinned with "go get" in a melange
// config, in the order they appear.
func parseGoModules(data []byte) []goModule {
	var mods []goModule
	for _, m := range goGetRe.FindAllSubmatch(data, -1) {
		mods = append(mods, goModule{Path: string(m[1]), Version: string(m[2])})
	}
	return mods
}

// patchGoModule rewrites the version of a Go module pinned with "go get".
func patchGoModule(data []byte, m goModule, version string) ([]byte, error) {
	out, err := replaceOnce(data, `go get `+regexp.QuoteMeta(m.Path+"@"+m.Version), "go get "+m.Path+"@"+version)
	if err != nil {
		return nil, fmt.Errorf("patching %s: %w", m.Path, err)
	}
	return out, nil
}

// replaceOnce replaces the value of a single YAML line matching pattern,
// preserving its indentation. It fails unless exactly one line matches.
func replaceOnce(data []byte, pattern, replacement string) ([]byte, error) {
	re := regexp.MustCompile(`(?m)^([ \t]+)` + pattern + `[ \t]*$`)
	if n := len(re.FindAllIndex(data, -1)); n != 1 {
		return nil, fmt.Errorf("expected exactly one line matching %q, found %d", pattern, n)
	}
	return re.ReplaceAll(data, []byte("${1}"+strings.ReplaceAll(replacement, "$", "$$"))), nil
}

// compareVersions compares two dotted numeric versions such as "2.11.7" or
// "26.09", returning -1, 0 or 1. Missing components are treated as 0.
func compareVersions(a, b string) (int, error) {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		x, err := versionComponent(as, i)
		if err != nil {
			return 0, fmt.Errorf("version %q: %w", a, err)
		}
		y, err := versionComponent(bs, i)
		if err != nil {
			return 0, fmt.Errorf("version %q: %w", b, err)
		}
		if x != y {
			if x < y {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

func versionComponent(parts []string, i int) (int, error) {
	if i >= len(parts) {
		return 0, nil
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("component %q is not a non-negative integer", parts[i])
	}
	return n, nil
}
