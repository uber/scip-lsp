package idl

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	"github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"go.uber.org/yarpc/encoding/protobuf"
	"go.uber.org/yarpc/yarpcerrors"
)

func TestToProtoErrorInvalidArgument(t *testing.T) {
	tests := []struct {
		name string
		want error
	}{
		{
			name: "no uuid on wire",
			want: errors.NoUUIDOnWireError,
		},
		{
			name: "no message on wire",
			want: errors.NoMessageOnWireError,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			e := ToProtoError(tt.want)
			require.Error(t, e)
			require.True(t, yarpcerrors.IsInvalidArgument(e))

			details := protobuf.GetErrorDetails(e)
			require.Len(t, details, 1)

			detail, ok := details[0].(*pb.UlspDaemonErrorDetails)
			require.True(t, ok)
			badRequest := detail.GetBadRequest()
			require.NotNil(t, badRequest)
			assert.Equal(t, badRequest.Msg, tt.want.Error())
		})
	}
}

func TestToProtoErrorNotFound(t *testing.T) {
	id := uuid.Must(uuid.NewV4())
	nf := &errors.UUIDNotFoundError{UUID: id}
	e := ToProtoError(nf)
	require.Error(t, e)
	require.True(t, yarpcerrors.IsNotFound(e))

	details := protobuf.GetErrorDetails(e)
	require.Len(t, details, 1)

	detail, ok := details[0].(*pb.UlspDaemonErrorDetails)
	require.True(t, ok)
	badRequest := detail.GetNotFound()
	require.NotNil(t, badRequest)
	assert.Equal(t, badRequest.Uuid, id.String())
}

func TestToProtoErrorUnknown(t *testing.T) {
	unknown := errors.New("unknown")
	e := ToProtoError(unknown)
	require.Error(t, e)
	assert.ErrorIs(t, e, unknown)
}
