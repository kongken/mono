package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
)

// toolError is a failed tool call, reported to the model as an MCP tool error
// result (isError) rather than a protocol error, so it can read the reason and
// retry. Domain failures keep their typed details: field errors, the current
// revision, or what still references a record.
type toolError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details []json.RawMessage `json:"details,omitempty"`
}

func (e *toolError) text() string {
	b, _ := json.Marshal(map[string]any{"error": e})
	return string(b)
}

func badArguments(err error) *toolError {
	return &toolError{Code: "INVALID_ARGUMENT", Message: "invalid arguments: " + err.Error()}
}

// decodeStrict decodes args into v, refusing unknown fields. Empty arguments
// decode as an empty object.
func decodeStrict(args json.RawMessage, v any) error {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the arguments")
	}
	return nil
}

// fromRPCError maps a metro status. Failures a client can act on carry their
// message and details; anything else is reported without internals.
func fromRPCError(err error) *toolError {
	st := status.Convert(err)
	switch st.Code() {
	case codes.InvalidArgument, codes.AlreadyExists, codes.FailedPrecondition, codes.NotFound:
		out := &toolError{Code: codeName(st.Code()), Message: st.Message()}
		for _, d := range st.Details() {
			var m proto.Message
			switch v := d.(type) {
			case protoadapt.MessageV1:
				m = protoadapt.MessageV2Of(v)
			case proto.Message:
				m = v
			default:
				continue
			}
			b, err := protojson.Marshal(m)
			if err != nil {
				continue
			}
			wrapped, _ := json.Marshal(map[string]json.RawMessage{
				string(m.ProtoReflect().Descriptor().Name()): b,
			})
			out.Details = append(out.Details, wrapped)
		}
		return out
	case codes.PermissionDenied, codes.Unauthenticated:
		return &toolError{Code: codeName(st.Code()), Message: "the management token is not allowed to do this"}
	case codes.DeadlineExceeded, codes.Canceled:
		return &toolError{Code: "DEADLINE_EXCEEDED", Message: "the request took too long; try again"}
	}
	slog.Error("mcp upstream call", "code", st.Code(), "error", err)
	return &toolError{Code: "UNAVAILABLE", Message: "the metro service is unavailable; try again later"}
}

func codeName(c codes.Code) string {
	switch c {
	case codes.InvalidArgument:
		return "INVALID_ARGUMENT"
	case codes.AlreadyExists:
		return "ALREADY_EXISTS"
	case codes.FailedPrecondition:
		return "FAILED_PRECONDITION"
	case codes.NotFound:
		return "NOT_FOUND"
	case codes.PermissionDenied:
		return "PERMISSION_DENIED"
	case codes.Unauthenticated:
		return "UNAUTHENTICATED"
	}
	return "UNKNOWN"
}
