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

func TestJavaTestExplorerExecute(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	a := ActionJavaTestExplorer{}

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
	assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java"}}`)))
}

func TestJavaTestExplorerProcessDocument(t *testing.T) {
	a := ActionJavaTestExplorer{}
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

func TestJavaTestExplorerProcessDocumentParameterizedTest(t *testing.T) {
	a := ActionJavaTestExplorer{}
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

func TestJavaTestExplorerCommandName(t *testing.T) {
	a := ActionJavaTestExplorer{}
	cmd := a.CommandName()
	assert.Equal(t, "", cmd)
}

func TestJavaTestExplorerShouldEnable(t *testing.T) {
	a := ActionJavaTestExplorer{}
	s := &entity.Session{
		UUID: factory.UUID(),
		InitializeParams: &protocol.InitializeParams{
			ClientInfo: &protocol.ClientInfo{
				Name: "",
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

func TestJavaTestExplorerIsRelevantDocument(t *testing.T) {
	a := ActionJavaTestExplorer{}

	relevantDoc := protocol.TextDocumentItem{URI: "file:///test.java", LanguageID: "java"}
	assert.True(t, a.IsRelevantDocument(nil, relevantDoc))

	irrelevantDoc := protocol.TextDocumentItem{URI: "file:///test.go", LanguageID: "go"}
	assert.False(t, a.IsRelevantDocument(nil, irrelevantDoc))
}

func TestJavaTestExplorerProvideWorkDoneProgressParams(t *testing.T) {
	a := ActionJavaTestExplorer{}

	providedParams, err := a.ProvideWorkDoneProgressParams(context.Background(), nil, nil)

	assert.NoError(t, err, "No error should be reported")
	assert.Nil(t, providedParams)
}
