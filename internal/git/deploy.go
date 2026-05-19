package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

type RepoInfo struct {
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	ComposeFile string `json:"compose_file,omitempty"`
}

func Clone(repoURL, branch, token string) (*RepoInfo, error) {
	repoName := extractRepoName(repoURL)
	repoPath := filepath.Join("/opt/dockpal/repos", repoName)

	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create repo directory: %w", err)
	}

	if err := os.RemoveAll(repoPath); err != nil {
		return nil, fmt.Errorf("failed to clean repo directory: %w", err)
	}

	cloneOpts := &git.CloneOptions{
		URL:          repoURL,
		Depth:        1,
		SingleBranch: true,
	}

	if token != "" {
		cloneOpts.Auth = &githttp.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	if branch != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(branch)
	}

	_, err := git.PlainClone(repoPath, false, cloneOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to clone repository: %w", err)
	}

	composeFile := detectComposeFile(repoPath)

	return &RepoInfo{
		Path:        repoPath,
		Branch:      branch,
		ComposeFile: composeFile,
	}, nil
}

func Pull(repoPath, token string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	pullOpts := &git.PullOptions{}
	if token != "" {
		pullOpts.Auth = &githttp.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	err = worktree.Pull(pullOpts)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to pull: %w", err)
	}

	return nil
}

func extractRepoName(url string) string {
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "repo"
}

func detectComposeFile(path string) string {
	candidates := []string{
		"docker-compose.yml",
		"docker-compose.yaml",
		"compose.yml",
		"compose.yaml",
	}

	for _, c := range candidates {
		fullPath := filepath.Join(path, c)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath
		}
	}

	return ""
}
