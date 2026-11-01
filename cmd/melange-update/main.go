// Command melange-update bumps a melange package to the latest release of its
// upstream GitHub repository.
//
// The upstream repository and tag format are read from the config's
// git-checkout step. When a newer release exists, the package version and
// expected-commit are updated and the epoch is reset to 0. Results are written
// as GitHub Actions step outputs: package, current, latest, tag, release-url
// and updated.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/sethvargo/go-envconfig"
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
	ctx = clog.WithValues(ctx, "package", src.Name, "upstream", src.Owner+"/"+src.Repo)

	gh := githubClient{token: cfg.GitHubToken}

	latestTag, err := gh.latestReleaseTag(ctx, src.Owner, src.Repo)
	if err != nil {
		clog.FatalContextf(ctx, "fetching latest release: %v", err)
	}
	latest, ok := src.VersionFromTag(latestTag)
	if !ok {
		clog.FatalContextf(ctx, "latest release tag %q does not match tag template %q", latestTag, src.TagTemplate)
	}

	cmp, err := compareVersions(src.Version, latest)
	if err != nil {
		clog.FatalContextf(ctx, "comparing versions: %v", err)
	}
	clog.InfoContext(ctx, "version check", "current", src.Version, "latest", latest)

	setOutput(ctx, cfg.GitHubOutput, "package", src.Name)
	setOutput(ctx, cfg.GitHubOutput, "current", src.Version)
	setOutput(ctx, cfg.GitHubOutput, "latest", latest)
	setOutput(ctx, cfg.GitHubOutput, "tag", latestTag)
	setOutput(ctx, cfg.GitHubOutput, "release-url", fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", src.Owner, src.Repo, url.PathEscape(latestTag)))

	if cmp >= 0 {
		clog.InfoContext(ctx, "already up to date")
		setOutput(ctx, cfg.GitHubOutput, "updated", "false")
		return
	}

	commit, err := gh.tagCommit(ctx, src.Owner, src.Repo, latestTag)
	if err != nil {
		clog.FatalContextf(ctx, "fetching commit for tag %s: %v", latestTag, err)
	}

	updated, err := patchConfig(data, src, latest, commit)
	if err != nil {
		clog.FatalContextf(ctx, "%s: %v", cfg.MelangeConfig, err)
	}
	if err := os.WriteFile(cfg.MelangeConfig, updated, 0o644); err != nil {
		clog.FatalContextf(ctx, "writing melange config: %v", err)
	}

	clog.InfoContext(ctx, "updated melange config", "version", latest, "commit", commit)
	setOutput(ctx, cfg.GitHubOutput, "updated", "true")
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
	u := "https://api.github.com/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned %s for %s", resp.Status, u)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

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
	fmt.Fprintf(f, "%s=%s\n", key, value)
}
