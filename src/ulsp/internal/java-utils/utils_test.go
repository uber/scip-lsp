package javautils

import (
	"os"
	"testing"

	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock/helpers"
	"go.uber.org/mock/gomock"

	"github.com/stretchr/testify/assert"
	"go.lsp.dev/protocol"
)

func TestJavaUtilsGetJavaTarget(t *testing.T) {
	workspaceRoot := "/home/user/fievel"
	t.Run("valid src", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/tooling/intellij/uber-intellij-plugin-core").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(8).Return([]os.DirEntry{}, nil)

		validDoc := protocol.DocumentURI("file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java")
		target, err := GetJavaTarget(fs, workspaceRoot, validDoc)
		assert.NoError(t, err)
		assert.Equal(t, "tooling/intellij/uber-intellij-plugin-core/...", target)
	})

	t.Run("valid test doc", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/roadrunner/application-dw").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(9).Return([]os.DirEntry{}, nil)

		validDoc := protocol.DocumentURI("file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java")
		target, err := GetJavaTarget(fs, workspaceRoot, validDoc)
		assert.NoError(t, err)
		assert.Equal(t, "roadrunner/application-dw/...", target)
	})

	t.Run("missing workspace root", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		fs := fsmock.NewMockUlspFS(ctrl)

		invalidDoc := protocol.DocumentURI("file:///home/roadrunner/application-dw/src/test/GatewayErrorExceptionMapperTest.java")
		_, err := GetJavaTarget(fs, workspaceRoot, invalidDoc)
		assert.Error(t, err, "uri /home/roadrunner/application-dw/src/test/GatewayErrorExceptionMapperTest.java is not a child of the workspace /home/user/fievel")
	})

	t.Run("missing src", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(false, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{}, nil)

		invalidDoc := protocol.DocumentURI("file:///home/user/fievel/roadrunner/GatewayErrorExceptionMapperTest.java")
		_, err := GetJavaTarget(fs, workspaceRoot, invalidDoc)
		assert.Error(t, err, "no child directory contained a BUILD file")
	})

	t.Run("pseudo bazel-out file", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		fs := fsmock.NewMockUlspFS(ctrl)

		validDoc := protocol.DocumentURI("file:///tmp/bazel_user/abcdef1234567890/bazel-out/k8-fastbuild/bin/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java")
		_, err := GetJavaTarget(fs, workspaceRoot, validDoc)
		assert.Error(t, err, "uri /tmp/bazel_user/abcdef1234567890/bazel-out/k8-fastbuild/bin/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java is not a child of the workspace /home/user/fievel")
	})
}

func TestNormalizeIDEClient(t *testing.T) {
	assert.Equal(t, "vscode", NormalizeIDEClient("Visual Studio Code"))
	assert.Equal(t, "cursor", NormalizeIDEClient("Cursor"))
	assert.Equal(t, "intellij", NormalizeIDEClient("intellij"))
}
