package quickactions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor/executormock"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.uber.org/mock/gomock"
)

func TestJavaTestExplorerInfoExecute(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	a := ActionJavaTestExplorerInfo{}

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = "/home/user/fievel"
	s.Monorepo = "lm/fievel"

	executorMock := executormock.NewMockExecutor(ctrl)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	c := &action.ExecuteParams{
		IdeGateway: ideGatewayMock,
		Sessions:   sessionRepository,
		Executor:   executorMock,
	}

	ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, params *protocol.LogMessageParams) error {
		assert.Equal(t, _messageTestExplorerDetails, params.Message)
		return nil
	})
	assert.NoError(t, a.Execute(ctx, c, []byte(`{}`)))
}

func TestJavaTestExplorerInfoProcessDocument(t *testing.T) {
	a := ActionJavaTestExplorerInfo{}
	doc := protocol.TextDocumentItem{
		URI:        "file:///MyExampleTest.java",
		LanguageID: "java",
		Text: `package com.uber.rider.growth.jobs;

import com.uber.fievel.testing.base.FievelTestBase;
import org.junit.Test;

public class MyExampleTest extends FievelTestBase {

	@Test
	public void myTestMethod() throws Exception {}
}

	`}
	results, err := a.ProcessDocument(context.Background(), doc)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(results))
	for _, result := range results {
		args := result.(protocol.CodeLens).Command.Arguments[0]
		assert.Empty(t, args)
	}
}

func TestJavaTestExplorerInfoProcessDocumentParameterizedTest(t *testing.T) {
	a := ActionJavaTestExplorerInfo{}
	doc := protocol.TextDocumentItem{
		URI:        "file:///MyExampleTest.java",
		LanguageID: "java",
		Text: `package com.uber.rider.growth.jobs;

import com.uber.fievel.testing.base.FievelTestBase;
import org.junit.Test;

public class MyExampleParamTest extends FievelTestBase {

  @Test
  @Parameters(method = "testAcceptOfferHandlerParams")
  public void myTesParamMethod(GrpcTestCaseX testCase) throws IOException {
}

	`}
	results, err := a.ProcessDocument(context.Background(), doc)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(results))
	for _, result := range results {
		args := result.(protocol.CodeLens).Command.Arguments[0]
		assert.Empty(t, args)
	}
}

func TestJavaTestExplorerInfoCommandName(t *testing.T) {
	a := ActionJavaTestExplorerInfo{}
	cmd := a.CommandName()
	assert.Equal(t, "ulsp.quick-actions.javatestexplorerinfo", cmd)
}

func TestJavaTestExplorerInfoShouldEnable(t *testing.T) {
	a := ActionJavaTestExplorerInfo{}
	s := &entity.Session{
		UUID: factory.UUID(),
		InitializeParams: &protocol.InitializeParams{
			ClientInfo: &protocol.ClientInfo{
				Name: "Unknown",
			},
		},
	}
	mce := entity.MonorepoConfigEntry{}

	assert.False(t, a.ShouldEnable(s, mce))

	mce.Languages = []string{"java"}
	assert.False(t, a.ShouldEnable(s, mce))

	s.InitializeParams.ClientInfo.Name = string(entity.ClientNameVSCode)
	assert.True(t, a.ShouldEnable(s, mce))
}

func TestJavaTestExplorerInfoIsRelevantDocument(t *testing.T) {
	a := ActionJavaTestExplorerInfo{}

	relevantDoc := protocol.TextDocumentItem{URI: "file:///test.java", LanguageID: "java"}
	assert.True(t, a.IsRelevantDocument(nil, relevantDoc))

	irrelevantDoc := protocol.TextDocumentItem{URI: "file:///test.go", LanguageID: "go"}
	assert.False(t, a.IsRelevantDocument(nil, irrelevantDoc))
}

func TestJavaTestExplorerInfoProvideWorkDoneProgressParams(t *testing.T) {
	a := ActionJavaTestExplorerInfo{}

	providedParams, err := a.ProvideWorkDoneProgressParams(context.Background(), nil, nil)

	assert.NoError(t, err, "No error should be reported")
	assert.Nil(t, providedParams)
}
