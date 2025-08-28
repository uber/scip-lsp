package javautils

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

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
func getBuildFile(workspaceRoot string, uri protocol.DocumentURI, includeBuildFile bool) (string, error) {
	filename := uri.Filename()

	if !strings.HasPrefix(filename, workspaceRoot) {
		return "", fmt.Errorf("uri %s is not a child of the workspace %s", filename, workspaceRoot)
	}

	info, err := os.Stat(filename)
	if err != nil {
		return "", err
	}

	currentDir := filename
	if !info.IsDir() {
		currentDir = filepath.Dir(currentDir)
	}

	for currentDir != workspaceRoot {
		children, err := os.ReadDir(currentDir)
		if err != nil {
			return "", err
		}

		for _, child := range children {
			if isBuildFile(child.Name()) {
				if includeBuildFile {
					return currentDir + string(filepath.Separator) + child.Name(), nil
				} else {
					return currentDir, nil
				}
			}
		}

		currentDir = filepath.Dir(currentDir)
	}

	return "", fmt.Errorf("no child directory contained a BUILD file")
}

// GetJavaTarget returns the target path for the given document URI.
func GetJavaTarget(workspaceRoot string, docURI protocol.DocumentURI) (string, error) {
	buildFileDir, err := getBuildFile(workspaceRoot, docURI, false)
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
