package javautils

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/uber/scip-lsp/src/ulsp/internal/fs"

	"go.lsp.dev/protocol"
)

const _targetSuffix = "..."
const _visualStudioCodeIDEClient = "Visual Studio Code"
const _cursorIDEClient = "Cursor"

var _buildFileNames = map[string]interface{}{"BUILD.bazel": nil, "BUILD": nil}

func isBuildFile(fileName string) bool {
	_, ok := _buildFileNames[fileName]
	return ok
}

// getBuildFile returns the absolute path of closest build file or build file directory to the URI, returning an error if the workspaceRoot is reached
func getBuildFile(fs fs.UlspFS, workspaceRoot string, uri protocol.DocumentURI, includeBuildFile bool) (string, error) {
	filename := uri.Filename()

	if !strings.HasPrefix(filename, workspaceRoot) {
		return "", fmt.Errorf("uri %s is not a child of the workspace %s", filename, workspaceRoot)
	}

	isDir, err := fs.DirExists(filename)
	if err != nil {
		return "", err
	}

	currentDir := filename
	if !isDir {
		currentDir = filepath.Dir(currentDir)
	}

	for currentDir != workspaceRoot {
		children, err := fs.ReadDir(currentDir)
		if err != nil {
			return "", err
		}

		for _, child := range children {
			childName := child.Name()
			if isBuildFile(childName) {
				if includeBuildFile {
					return currentDir + string(filepath.Separator) + childName, nil
				} else {
					return currentDir, nil
				}
			}
		}

		currentDir = filepath.Dir(currentDir)
	}

	return "", fmt.Errorf("no child directory contained a BUILD file")
}

// GetJavaTarget returns the bazel target for all targets in a path for the given document URI by finding the nearest
// parent BUILD.bazel file and appending the `...` suffix to the path.
// Example: for a document URI of /home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java
// and a workspace root of /home/user/fievel, the returned target would be tooling/intellij/uber-intellij-plugin-core/...
// assuming that the BUILD.bazel file is located at /home/user/fievel/tooling/intellij/uber-intellij-plugin-core/BUILD.bazel
func GetJavaTarget(fs fs.UlspFS, workspaceRoot string, docURI protocol.DocumentURI) (string, error) {
	buildFileDir, err := getBuildFile(fs, workspaceRoot, docURI, false)
	if err != nil {
		return "", err
	}

	targetSegments := strings.SplitN(buildFileDir, workspaceRoot+string(filepath.Separator), 2)
	if len(targetSegments) != 2 {
		return "", fmt.Errorf("missing workspace root")
	}

	target := path.Join(targetSegments[1], _targetSuffix)
	return target, nil
}

// NormalizeIDEClient normalizes the IDE client name to a consistent format.
func NormalizeIDEClient(ideClient string) string {
	if ideClient == _visualStudioCodeIDEClient {
		return "vscode"
	} else if ideClient == _cursorIDEClient {
		return "cursor"
	}
	return ideClient
}
