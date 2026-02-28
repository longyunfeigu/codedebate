// Package detect 提供平台自动检测功能。
// 根据 git remote URL 或用户配置判断当前项目使用的代码托管平台。
package detect

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/longyunfeigu/codedebate/internal/platform"
	gh "github.com/longyunfeigu/codedebate/internal/platform/github"
	gl "github.com/longyunfeigu/codedebate/internal/platform/gitlab"
)

// githubRemoteRegex 匹配 GitHub 远程 URL
var githubRemoteRegex = regexp.MustCompile(`github\.com[:/]`)

// gitlabRemoteRegex 匹配 GitLab 远程 URL
var gitlabRemoteRegex = regexp.MustCompile(`gitlab\.com[:/]`)

// FromRemote 根据 git remote URL 自动检测平台类型。
// platformType 可为 "auto"(默认)、"github"、"gitlab"。
// customHost 用于自托管 GitLab 域名匹配。
func FromRemote(platformType, customHost string) (platform.Platform, error) {
	switch strings.ToLower(platformType) {
	case "github":
		return gh.New(), nil
	case "gitlab":
		return gl.New(customHost), nil
	}

	// 自动检测
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return nil, fmt.Errorf("could not detect platform: failed to get git remote URL: %w", err)
	}
	remoteURL := strings.TrimSpace(string(out))

	if githubRemoteRegex.MatchString(remoteURL) {
		return gh.New(), nil
	}

	if gitlabRemoteRegex.MatchString(remoteURL) {
		return gl.New(""), nil
	}

	if customHost != "" {
		hostPattern := regexp.MustCompile(regexp.QuoteMeta(customHost) + `[:/]`)
		if hostPattern.MatchString(remoteURL) {
			return gl.New(customHost), nil
		}
	}

	return nil, fmt.Errorf("could not detect platform from git remote URL: %s (supported: github.com, gitlab.com)", remoteURL)
}
