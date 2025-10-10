package jdk

import (
	"context"
	"errors"
	"testing"

	osscip "github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	"github.com/uber/scip-lsp/src/ulsp/controller/jdk/types"
	"github.com/uber/scip-lsp/src/ulsp/controller/scip"
	"github.com/uber/scip-lsp/src/ulsp/controller/scip/scipmock"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/config"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestResolveBreakpoints(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string

		getFileInfoErr  error
		readFileErr     error
		mockDefinitions []*model.SymbolOccurrence
		request         *types.ResolveBreakpoints

		wantErr  bool
		errorMsg string
		response []*types.BreakpointLocation
	}{
		{
			name: "no defs",
			request: &types.ResolveBreakpoints{
				SourceURI: "file:///path/to/file/String.java",
				Breakpoints: []*protocol.Position{
					{
						Line:      1,
						Character: 0,
					},
				},
			},
			mockDefinitions: []*model.SymbolOccurrence{},

			wantErr:  false,
			response: []*types.BreakpointLocation{},
		},
		{
			name: "no class",
			request: &types.ResolveBreakpoints{
				SourceURI: "file:///path/to/file/String.java",
				Breakpoints: []*protocol.Position{
					{
						Line:      1,
						Character: 0,
					},
				},
			},
			mockDefinitions: []*model.SymbolOccurrence{
				{
					Info: &model.SymbolInformation{
						Kind:   osscip.SymbolInformation_Constant,
						Symbol: "scip-java . . . com/example/Baz#",
					},
					Occurrence: &model.Occurrence{
						Symbol: "scip-java . . . com/example/Baz#",
						Range:  []int32{1, 1, 1, 10},
					},
					Location: uri.File("/path/to/file/String.java"),
				},
			},

			wantErr:  false,
			response: []*types.BreakpointLocation{},
		},
		{
			name: "single class",
			request: &types.ResolveBreakpoints{
				SourceURI: "file:///path/to/file/String.java",
				Breakpoints: []*protocol.Position{
					{
						Line:      1,
						Character: 0,
					},
				},
			},
			mockDefinitions: []*model.SymbolOccurrence{
				{
					Info: &model.SymbolInformation{
						Kind:   osscip.SymbolInformation_Class,
						Symbol: "scip-java . . . java/String#",
					},
					Occurrence: &model.Occurrence{
						Symbol: "scip-java . . . java/String#",
						Range:  []int32{1, 1, 1, 10},
					},
					Location: uri.File("/path/to/file/String.java"),
				},
			},

			wantErr: false,
			response: []*types.BreakpointLocation{
				&types.BreakpointLocation{
					Line:      1,
					Column:    0,
					ClassName: "java.String",
				},
			},
		},
		{
			name: "multi class",
			request: &types.ResolveBreakpoints{
				SourceURI: "file:///path/to/file.java",
				Breakpoints: []*protocol.Position{
					{
						Line:      1,
						Character: 0,
					},
				},
			},
			mockDefinitions: []*model.SymbolOccurrence{
				{
					Info: &model.SymbolInformation{
						Kind:   osscip.SymbolInformation_Class,
						Symbol: "scip-java . . . com/example/Foo#",
					},
					Occurrence: &model.Occurrence{
						Symbol: "scip-java . . . com/example/Foo#",
						Range:  []int32{1, 1, 1, 10},
					},
					Location: uri.File("/path/to/file.java"),
				},
				{
					Info: &model.SymbolInformation{
						Kind:   osscip.SymbolInformation_Class,
						Symbol: "scip-java . . . com/example/Foo#Bar#",
					},
					Occurrence: &model.Occurrence{
						Symbol: "scip-java . . . com/example/Foo#Bar#",
						Range:  []int32{1, 1, 1, 10},
					},
					Location: uri.File("/path/to/file.java"),
				},
				{
					Info: &model.SymbolInformation{
						Kind:   osscip.SymbolInformation_Constant,
						Symbol: "scip-java . . . com/example/Baz#",
					},
					Occurrence: &model.Occurrence{
						Symbol: "scip-java . . . com/example/Baz#",
						Range:  []int32{1, 1, 1, 10},
					},
					Location: uri.File("/path/to/file.java"),
				},
			},

			wantErr: false,
			response: []*types.BreakpointLocation{
				&types.BreakpointLocation{
					Line:      1,
					Column:    0,
					ClassName: "com.example.Foo", // Should be com.example.Foo$Bar
				},
			},
		},
		{
			name: "no file info",
			request: &types.ResolveBreakpoints{
				SourceURI: "file:///path/to/file.java",
				Breakpoints: []*protocol.Position{
					{
						Line:      1,
						Character: 0,
					},
				},
			},

			wantErr:  false,
			response: []*types.BreakpointLocation{},
		},
		{
			name: "file info error",
			request: &types.ResolveBreakpoints{
				SourceURI: "file:///path/to/file.java",
				Breakpoints: []*protocol.Position{
					{
						Line:      1,
						Character: 0,
					},
				},
			},

			getFileInfoErr: errors.New("failed to read index"),
			wantErr:        true,
			errorMsg:       "failed to read index",
			response:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			scCtrl := scipmock.NewMockController(ctrl)
			mockFs := fsmock.NewMockUlspFS(ctrl)
			var err error
			c := controller{
				sessions:   repositorymock.NewMockRepository(ctrl),
				ideGateway: ideclientmock.NewMockGateway(ctrl),
				logger:     zap.NewNop().Sugar(),
				stats:      tally.NewTestScope("testing", make(map[string]string, 0)),

				scip: scCtrl,
				fs:   mockFs,
			}
			if tt.mockDefinitions != nil {
				scCtrl.EXPECT().GetDocumentSymbols(gomock.Any(), gomock.Any(), tt.request.SourceURI).Return(tt.mockDefinitions, nil)
			} else {
				scCtrl.EXPECT().GetDocumentSymbols(gomock.Any(), gomock.Any(), tt.request.SourceURI).Return(nil, tt.getFileInfoErr)
			}

			bps, err := c.ResolveBreakpoints(ctx, tt.request)
			if tt.wantErr {
				assert.ErrorContains(t, err, tt.errorMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.response, bps)
			}
		})
	}

}

func TestResolveClassToPath(t *testing.T) {
	tests := []struct {
		name           string
		request        *types.ResolveClassToPath
		symbol         *model.SymbolOccurrence
		packageInfoErr error

		expectedDescriptors []model.Descriptor
		wantErr             bool
		response            string
	}{
		{
			name: "resolve valid class",
			expectedDescriptors: []model.Descriptor{
				{
					Name:   "com",
					Suffix: osscip.Descriptor_Namespace,
				},
				{
					Name:   "example",
					Suffix: osscip.Descriptor_Namespace,
				},
				{
					Name:   "Foo",
					Suffix: osscip.Descriptor_Type,
				},
			},
			symbol: &model.SymbolOccurrence{
				Location: "file:///path/to/file/Foo.java",
				Info: &model.SymbolInformation{
					Kind:   osscip.SymbolInformation_Class,
					Symbol: "scip-java . . . com/example/Foo#",
				},
				Occurrence: &model.Occurrence{
					Symbol: "scip-java . . . com/example/Foo#",
					Range:  []int32{1, 1, 1, 10},
				},
			},
			request: &types.ResolveClassToPath{
				FullyQualifiedName: "com.example.Foo",
				SourceRelativePath: "Foo.java",
			},

			wantErr:  false,
			response: "file:///path/to/file/Foo.java",
		},
		{
			name: "resolve valid jdk class",
			expectedDescriptors: []model.Descriptor{
				{
					Name:   "java",
					Suffix: osscip.Descriptor_Namespace,
				},
				{
					Name:   "String",
					Suffix: osscip.Descriptor_Type,
				},
			},
			request: &types.ResolveClassToPath{
				FullyQualifiedName: "java.String",
				SourceRelativePath: "String.java",
			},
			symbol: &model.SymbolOccurrence{
				Location: "file:///path/to/file/String.java",
				Info: &model.SymbolInformation{
					Kind:   osscip.SymbolInformation_Class,
					Symbol: "scip-java . . . java/String#",
				},
				Occurrence: &model.Occurrence{
					Symbol: "scip-java . . . java/String#",
					Range:  []int32{1, 1, 1, 10},
				},
			},
			wantErr:  false,
			response: "file:///path/to/file/String.java",
		},
		{
			name: "class doesn't exist",
			expectedDescriptors: []model.Descriptor{
				{
					Name:   "com",
					Suffix: osscip.Descriptor_Namespace,
				},
				{
					Name:   "example",
					Suffix: osscip.Descriptor_Namespace,
				},
				{
					Name:   "Invalid",
					Suffix: osscip.Descriptor_Type,
				},
			},
			request: &types.ResolveClassToPath{
				FullyQualifiedName: "com.example.Invalid",
				SourceRelativePath: "Invalid.java",
			},
			symbol:         nil,
			packageInfoErr: errors.New("class not found"),
			wantErr:        true,
		},
		{
			name: "empty FQN",

			request: &types.ResolveClassToPath{
				FullyQualifiedName: "",
				SourceRelativePath: "Foo.java",
			},
			wantErr: true,
		},
	}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			scipControllerMock := scipmock.NewMockController(ctrl)
			c := controller{
				scip: scipControllerMock,
			}

			if tt.symbol != nil || tt.packageInfoErr != nil {
				scipControllerMock.EXPECT().GetSymbolDefinitionOccurrence(ctx, gomock.Any(), tt.expectedDescriptors, gomock.Any()).Return(tt.symbol, tt.packageInfoErr)
			}

			file, err := c.ResolveClassToPath(ctx, tt.request)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.response, file)
			}
		})
	}
}

func TestGetFullClassName(t *testing.T) {
	tests := []struct {
		name   string
		symbol string
		want   string
	}{
		{
			name:   "simple class",
			symbol: "scip-java . . . com/example/Foo#",
			want:   "com.example.Foo",
		},
		{
			name:   "inner class",
			symbol: "scip-java . . . com/example/Foo#Bar#",
			want:   "com.example.Foo$Bar",
		},
		{
			name:   "multiple inner classes",
			symbol: "scip-java . . . com/example/Foo#Bar#Baz#",
			want:   "com.example.Foo$Bar$Baz",
		},
		{
			name:   "namespace only",
			symbol: "scip-java . . . com/example/",
			want:   "com.example.",
		},
		{
			name:   "empty symbol",
			symbol: "",
			want:   "",
		},
		{
			name:   "invalid symbol",
			symbol: "invalid-symbol",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getFullClassName(tt.symbol)
			assert.Equal(t, tt.want, got)
		})
	}
}
func TestGetPackageAndClass(t *testing.T) {
	tests := []struct {
		name      string
		fqn       string
		wantPkg   string
		wantClass string
	}{
		{
			name:      "fully qualified name",
			fqn:       "com.example.Foo",
			wantPkg:   "com.example",
			wantClass: "Foo",
		},
		{
			name:      "single class name",
			fqn:       "Foo",
			wantPkg:   "",
			wantClass: "Foo",
		},
		{
			name:      "empty string",
			fqn:       "",
			wantPkg:   "",
			wantClass: "",
		},
		{
			name:      "package only",
			fqn:       "com.example.",
			wantPkg:   "com.example",
			wantClass: "",
		},
		{
			name:      "nested class",
			fqn:       "com.example.Outer$Inner",
			wantPkg:   "com.example",
			wantClass: "Outer$Inner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPkg, gotClass := getPackageAndClass(tt.fqn)
			assert.Equal(t, tt.wantPkg, gotPkg)
			assert.Equal(t, tt.wantClass, gotClass)
		})
	}
}

func TestNew(t *testing.T) {
	mockConfig, _ := config.NewStaticProvider(map[string]interface{}{})
	assert.NotPanics(t, func() {
		New(Params{
			Stats:  tally.NewTestScope("testing", make(map[string]string, 0)),
			Config: mockConfig,
			Logger: zap.NewNop().Sugar(),
		})
	})
}

func TestStartupInfo(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	result, err := c.StartupInfo(ctx)

	assert.NoError(t, err)
	assert.NoError(t, result.Validate())
	assert.Equal(t, _nameKey, result.NameKey)
}

func TestExit(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	c := controller{
		sessions:   repositorymock.NewMockRepository(ctrl),
		ideGateway: ideclientmock.NewMockGateway(ctrl),
		logger:     zap.NewNop().Sugar(),
		stats:      tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	err := c.exit(ctx)
	assert.NoError(t, err)
}

func getMockFileInfo() *scip.FileInfo {
	fi := scip.FileInfo{
		Definitions: getMockExamplePackage().SymbolData,
	}

	return &fi
}

func getMockExamplePackage() *scip.PackageMeta {
	mockPackageMetaEx := *scip.NewScipPackage(&model.ScipPackage{
		Manager: "maven",
		Name:    "com/example",
		Version: "0.0.1",
	})
	mockPackageMetaEx.SymbolData["scip-java . . . com/example/Foo#"] = scip.NewSymbolData(
		&model.SymbolInformation{
			Kind:   osscip.SymbolInformation_Class,
			Symbol: "scip-java . . . com/example/Foo#",
		},
		nil,
		&protocol.LocationLink{
			TargetURI: "file:///path/to/file/Foo.java",
		},
	)
	mockPackageMetaEx.SymbolData["scip-java . . . com/example/Foo#Bar#"] = scip.NewSymbolData(
		&model.SymbolInformation{
			Kind:   osscip.SymbolInformation_Class,
			Symbol: "scip-java . . . com/example/Foo#Bar#",
		},
		nil,
		&protocol.LocationLink{
			TargetURI: "file:///path/to/file/Bar.java",
		},
	)
	mockPackageMetaEx.SymbolData["scip-java . . . com/example/Baz#"] = scip.NewSymbolData(
		&model.SymbolInformation{
			Kind:   osscip.SymbolInformation_Constant,
			Symbol: "scip-java . . . com/example/Baz#",
		},
		nil,
		&protocol.LocationLink{
			TargetURI: "file:///path/to/file/Baz.java",
		},
	)
	mockPackageMetaEx.SymbolData["scip-java . . . com/example/NoSymbInfo#"] = scip.NewSymbolData(
		nil,
		nil,
		&protocol.LocationLink{
			TargetURI: "file:///path/to/file/Baz.java",
		},
	)

	return &mockPackageMetaEx
}

func getMockJdkPackage() *scip.PackageMeta {
	mockPackageMetaJdk := *scip.NewScipPackage(&model.ScipPackage{
		Manager: "maven",
		Name:    "java",
		Version: "17",
	})
	mockPackageMetaJdk.SymbolData["scip-java . . . java/String#"] = scip.NewSymbolData(
		&model.SymbolInformation{
			Kind:   osscip.SymbolInformation_Class,
			Symbol: "scip-java . . . java/String#",
		},
		nil,
		&protocol.LocationLink{
			TargetURI: "file:///path/to/file/String.java",
		},
	)
	mockPackageMetaJdk.SymbolData["scip-java . . . java/Object#"] = scip.NewSymbolData(
		&model.SymbolInformation{
			Kind:   osscip.SymbolInformation_Constant,
			Symbol: "scip-java . . . java/Object#",
		},
		nil,
		&protocol.LocationLink{
			TargetURI: "file:///path/to/file/Object.java",
		},
	)

	return &mockPackageMetaJdk
}
