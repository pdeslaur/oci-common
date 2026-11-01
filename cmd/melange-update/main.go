// Command melange-update bumps a melange package and the Go modules it pins to
// their latest releases.
//
// The upstream repository and tag format are read from the config's
// git-checkout step. When a newer GitHub release exists, the package version
// and expected-commit are updated. Go modules pinned in pipeline steps with
// "go get MODULE@VERSION" are bumped to the latest release within their major
// version, as reported by the Go module proxy.
//
// When the package version changes the epoch is reset to 0; when only Go
// modules change it is incremented. Results are written as GitHub Actions step
// outputs: package, updated, and when updated, title and body.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/sethvargo/go-envconfig"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type config struct {
	GitHubToken   string `env:"GH_TOKEN,required"`
	MelangeConfig string `env:"MELANGE_CONFIG,default=melange.yaml"`
	GitHubOutput  string `env:"GITHUB_OUTPUT"`
}

func main() {
	ctx := context.Background()
	cfg := envconfig.MustProcess(ctx, &config{})

	data, err := os.ReadFile(cfg.MelangeConfig)
	if err != nil {
		clog.FatalContextf(ctx, "reading melange config: %v", err)
	}

	src, err := parseSource(data)
	if err != nil {
		clog.FatalContextf(ctx, "%s: %v", cfg.MelangeConfig, err)
	}
	ctx = clog.WithValues(ctx, "package", src.Name)
	setOutput(ctx, cfg.GitHubOutput, "package", src.Name)

	var updates []update

	data, pkgUpdate, err := updatePackage(ctx, githubClient{token: cfg.GitHubToken}, data, src)
	if err != nil {
		clog.FatalContextf(ctx, "updating package: %v", err)
	}
	if pkgUpdate != nil {
		updates = append(updates, *pkgUpdate)
	}

	data, modUpdates, err := updateGoModules(ctx, data)
	if err != nil {
		clog.FatalContextf(ctx, "updating Go modules: %v", err)
	}
	updates = append(updates, modUpdates...)

	if len(updates) == 0 {
		clog.InfoContext(ctx, "already up to date")
		setOutput(ctx, cfg.GitHubOutput, "updated", "false")
		return
	}

	epoch := src.Epoch + 1
	if pkgUpdate != nil {
		epoch = 0
	}
	if data, err = setEpoch(data, epoch); err != nil {
		clog.FatalContextf(ctx, "%s: %v", cfg.MelangeConfig, err)
	}

	if err := os.WriteFile(cfg.MelangeConfig, data, 0o644); err != nil {
		clog.FatalContextf(ctx, "writing melange config: %v", err)
	}

	clog.InfoContext(ctx, "updated melange config", "updates", len(updates), "epoch", epoch)
	setOutput(ctx, cfg.GitHubOutput, "updated", "true")
	setOutput(ctx, cfg.GitHubOutput, "title", title(updates))
	setOutput(ctx, cfg.GitHubOutput, "body", body(src.Name, updates))
}

// updatePackage bumps the package to the latest upstream GitHub release. It
// returns a nil update if the package is already up to date.
func updatePackage(ctx context.Context, gh githubClient, data []byte, src source) ([]byte, *update, error) {
	ctx = clog.WithValues(ctx, "upstream", src.Owner+"/"+src.Repo)

	latestTag, err := gh.latestReleaseTag(ctx, src.Owner, src.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching latest release: %w", err)
	}
	latest, ok := src.VersionFromTag(latestTag)
	if !ok {
		return nil, nil, fmt.Errorf("latest release tag %q does not match tag template %q", latestTag, src.TagTemplate)
	}

	cmp, err := compareVersions(src.Version, latest)
	if err != nil {
		return nil, nil, fmt.Errorf("comparing versions: %w", err)
	}
	clog.InfoContext(ctx, "package version check", "current", src.Version, "latest", latest)
	if cmp >= 0 {
		return data, nil, nil
	}

	commit, err := gh.tagCommit(ctx, src.Owner, src.Repo, latestTag)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching commit for tag %s: %w", latestTag, err)
	}
	data, err = patchConfig(data, src, latest, commit)
	if err != nil {
		return nil, nil, err
	}
	return data, &update{
		Name: src.Name,
		From: src.Tag(src.Version),
		To:   latestTag,
		URL:  fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", src.Owner, src.Repo, url.PathEscape(latestTag)),
	}, nil
}

// updateGoModules bumps each Go module pinned with "go get" to its latest
// release within the same major version.
func updateGoModules(ctx context.Context, data []byte) ([]byte, []update, error) {
	var updates []update
	for _, m := range parseGoModules(data) {
		ctx := clog.WithValues(ctx, "module", m.Path)
		if !semver.IsValid(m.Version) || semver.Prerelease(m.Version) != "" {
			clog.WarnContext(ctx, "skipping module not pinned to a release version", "version", m.Version)
			continue
		}

		latest, err := latestModuleVersion(ctx, m.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("fetching latest version of %s: %w", m.Path, err)
		}
		clog.InfoContext(ctx, "module version check", "current", m.Version, "latest", latest)
		// The proxy reports a pseudo-version, which is a prerelease, for
		// modules without any tagged release.
		if semver.Prerelease(latest) != "" || semver.Compare(latest, m.Version) <= 0 {
			continue
		}

		if data, err = patchGoModule(data, m, latest); err != nil {
			return nil, nil, err
		}
		updates = append(updates, update{
			Name: m.Path,
			From: m.Version,
			To:   latest,
			URL:  "https://pkg.go.dev/" + m.Path + "@" + latest,
		})
	}
	return data, updates, nil
}

// latestModuleVersion returns the latest version of a Go module path from the
// Go module proxy. Since major versions above v1 have distinct module paths,
// this never crosses a major version.
func latestModuleVersion(ctx context.Context, path string) (string, error) {
	escaped, err := module.EscapePath(path)
	if err != nil {
		return "", err
	}
	var r struct {
		Version string `json:"Version"`
	}
	if err := getJSON(ctx, "https://proxy.golang.org/"+escaped+"/@latest", nil, &r); err != nil {
		return "", err
	}
	return r.Version, nil
}

type githubClient struct {
	token string
}

func (c githubClient) latestReleaseTag(ctx context.Context, owner, repo string) (string, error) {
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := c.get(ctx, fmt.Sprintf("repos/%s/%s/releases/latest", owner, repo), &r); err != nil {
		return "", err
	}
	return r.TagName, nil
}

func (c githubClient) tagCommit(ctx context.Context, owner, repo, tag string) (string, error) {
	var r struct {
		SHA string `json:"sha"`
	}
	if err := c.get(ctx, fmt.Sprintf("repos/%s/%s/commits/%s", owner, repo, url.PathEscape(tag)), &r); err != nil {
		return "", err
	}
	return r.SHA, nil
}

func (c githubClient) get(ctx context.Context, path string, out any) error {
	return getJSON(ctx, "https://api.github.com/"+path, http.Header{
		"Authorization": {"Bearer " + c.token},
		"Accept":        {"application/vnd.github+json"},
	}, out)
}

func getJSON(ctx context.Context, u string, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// setOutput writes a GitHub Actions step output, using the multiline
// delimiter syntax so that values may span several lines.
func setOutput(ctx context.Context, path, key, value string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		clog.WarnContextf(ctx, "opening GITHUB_OUTPUT: %v", err)
		return
	}
	defer f.Close()
	delim := make([]byte, 16)
	rand.Read(delim)
	d := "EOF_" + hex.EncodeToString(delim)
	fmt.Fprintf(f, "%s<<%s\n%s\n%s\n", key, d, value, d)
}
