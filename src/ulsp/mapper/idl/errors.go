package idl

import (
	"github.com/gofrs/uuid"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	"github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"go.uber.org/yarpc/encoding/protobuf"
	"go.uber.org/yarpc/yarpcerrors"
)

// ToProtoError translates service domain errors into a proto error details.
func ToProtoError(e error) error {
	if errors.IsBadRequest(e) {
		return toBadRequest(e)
	}

	if id, ok := errors.NotFoundUUID(e); ok {
		return toNotFound(id, e.Error())
	}

	return e
}

func toBadRequest(e error) error {
	return protobuf.NewError(
		yarpcerrors.CodeInvalidArgument, // this error is a 400 to callers
		e.Error(),                       // error string to send to callers
		protobuf.WithErrorDetails(
			&pb.UlspDaemonErrorDetails{ // typed metadata the caller can extract
				Type: &pb.UlspDaemonErrorDetails_BadRequest{
					BadRequest: &pb.BadRequest{Msg: e.Error()},
				},
			},
		),
	)
}

func toNotFound(id uuid.UUID, msg string) error {
	return protobuf.NewError(
		yarpcerrors.CodeNotFound, // this error is a 404 to callers
		msg,                      // error string to send to callers
		protobuf.WithErrorDetails(
			&pb.UlspDaemonErrorDetails{ // typed metadata the caller can extract
				Type: &pb.UlspDaemonErrorDetails_NotFound{
					NotFound: &pb.NotFound{Uuid: id.String()},
				},
			},
		),
	)
}
