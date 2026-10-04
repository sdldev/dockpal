package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/git"
	"github.com/sdldev/dockpal/internal/validator"
)

// gitDeployOptions carries what the three git deploy entry points (local
// /deploy/git, instance-scoped /deploy/git and the webhook redeploy) need to
// clone a repo and pick a compose file. The entry points differ only in the
// two flags; the rest is one shared code path.
type gitDeployOptions struct {
	Repo        string
	Branch      string
	ComposeFile string
	Name        string
	Token       string
	// AutoSelect uses the first compose file when ComposeFile is empty
	// instead of returning a select_compose response — webhook redeploys
	// have no user to answer the question.
	AutoSelect bool
	// TrustComposeFile skips the compose-files-list membership check for a
	// stored ComposeFile (webhooks); the path-prefix check still confines
	// the read to the clone.
	TrustComposeFile bool
}

// gitDeployPrep is the clone result the deploy callers continue from.
type gitDeployPrep struct {
	Info        *git.RepoInfo
	ComposeData string
	ProjectName string
}

// prepareGitDeploy clones opts.Repo, selects and reads the compose file and
// derives the project name. It writes the error or compose-selection response
// itself and returns nil when the caller must stop. Env injection, restart
// policy, registry auth resolution and the deploy call stay with the caller.
func prepareGitDeploy(c *gin.Context, opts gitDeployOptions) *gitDeployPrep {
	info, err := git.Clone(opts.Repo, opts.Branch, opts.Token)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "authentication") || strings.Contains(errMsg, "Authorization") ||
			strings.Contains(errMsg, "denied") || strings.Contains(errMsg, "not found") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication failed: repository not accessible. Add a GitHub credential in Settings > Registry with registry 'github.com' and a PAT with repo scope."})
			return nil
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to clone repository: %s", errMsg)})
		return nil
	}

	if len(info.ComposeFiles) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no docker-compose file found in repository"})
		return nil
	}

	selectedFile := opts.ComposeFile
	if selectedFile == "" {
		if len(info.ComposeFiles) > 1 && !opts.AutoSelect {
			c.JSON(http.StatusOK, gin.H{"status": "select_compose", "compose_files": info.ComposeFiles, "info": info})
			return nil
		}
		selectedFile = info.ComposeFiles[0]
	} else if !opts.TrustComposeFile {
		validFile := false
		for _, f := range info.ComposeFiles {
			if f == selectedFile {
				validFile = true
				break
			}
		}
		if !validFile {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("compose file '%s' not found in repository", selectedFile)})
			return nil
		}
	}

	composePath := filepath.Join(info.Path, selectedFile)
	// The compose file name is stored or request input; keep the read inside
	// the clone so a crafted value cannot pull host files into a deploy.
	if !strings.HasPrefix(composePath, filepath.Clean(info.Path)+string(filepath.Separator)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "compose file must be inside the repository"})
		return nil
	}
	composeData, err := os.ReadFile(composePath)
	if err != nil {
		internalError(c, err)
		return nil
	}

	projectName := opts.Name
	if projectName == "" {
		projectName = filepath.Base(info.Path)
	}
	if err := validator.ValidateContainerName(projectName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
		return nil
	}

	return &gitDeployPrep{Info: info, ComposeData: string(composeData), ProjectName: projectName}
}
