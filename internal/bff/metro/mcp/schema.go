package mcp

import (
	"google.golang.org/protobuf/reflect/protoreflect"
)

// inputSchema derives the JSON Schema of a tool's arguments from the protobuf
// request message it is decoded into, so the schema cannot drift from the
// contract. Property names are the protobuf JSON names (lowerCamelCase), the same
// ones the browser contract uses. Generated descriptors carry no comments, so
// the human guidance lives in the tool descriptions.
func inputSchema(md protoreflect.MessageDescriptor) map[string]any { return messageSchema(md, 0) }

const maxSchemaDepth = 8

func messageSchema(md protoreflect.MessageDescriptor, depth int) map[string]any {
	if md.FullName() == "google.protobuf.Timestamp" {
		return map[string]any{"type": "string", "format": "date-time", "description": "RFC 3339 time, for example 2026-01-31T00:00:00Z"}
	}
	out := map[string]any{"type": "object", "additionalProperties": false}
	if depth >= maxSchemaDepth {
		return out
	}
	props := map[string]any{}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		props[fd.JSONName()] = fieldSchema(fd, depth)
	}
	out["properties"] = props
	return out
}

func fieldSchema(fd protoreflect.FieldDescriptor, depth int) map[string]any {
	if fd.IsMap() {
		return map[string]any{"type": "object", "additionalProperties": fieldSchema(fd.MapValue(), depth+1)}
	}
	item := scalarSchema(fd, depth)
	if fd.IsList() {
		return map[string]any{"type": "array", "items": item}
	}
	return item
}

func scalarSchema(fd protoreflect.FieldDescriptor, depth int) map[string]any {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return map[string]any{"type": "integer"}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}
	case protoreflect.StringKind, protoreflect.BytesKind:
		return map[string]any{"type": "string"}
	case protoreflect.EnumKind:
		vals := fd.Enum().Values()
		names := make([]string, 0, vals.Len())
		for i := 0; i < vals.Len(); i++ {
			names = append(names, string(vals.Get(i).Name()))
		}
		return map[string]any{"type": "string", "enum": names}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return messageSchema(fd.Message(), depth+1)
	}
	return map[string]any{}
}
