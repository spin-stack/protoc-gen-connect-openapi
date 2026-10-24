package converter

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
	"github.com/pb33f/libopenapi/utils"
	"go.yaml.in/yaml/v4"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/sudorandom/protoc-gen-connect-openapi/internal/converter/options"
	"github.com/sudorandom/protoc-gen-connect-openapi/internal/converter/schema"
	"github.com/sudorandom/protoc-gen-connect-openapi/internal/converter/util"
	"github.com/sudorandom/protoc-gen-connect-openapi/internal/converter/visibility"
)

func AddMessageSchemas(opts options.Options, md protoreflect.MessageDescriptor, doc *v3.Document) {
	if md == nil {
		return
	}
	// Synthetic map-entry messages never appear in JSON: map fields are
	// rendered inline as objects with additionalProperties, so nothing can
	// reference an entry schema.
	if md.IsMapEntry() {
		return
	}
	if visibility.ShouldBeFiltered(visibility.GetVisibilityRule(md), opts.AllowedVisibilities) {
		opts.Logger.Debug("Filtering message due to visibility", slog.String("message", string(md.FullName())), slog.Any("restriction_selectors", opts.AllowedVisibilities))
		return
	}
	if _, ok := doc.Components.Schemas.Get(string(md.FullName())); ok {
		return
	}
	name, schema := schema.MessageToSchema(opts, md)
	if schema != nil {
		doc.Components.Schemas.Set(name, base.CreateSchemaProxy(schema))
	}

	// Messages can have fields
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		AddFieldToSchema(opts, fields.Get(i), doc)
	}

	// Messages can have enums
	enums := md.Enums()
	for i := 0; i < enums.Len(); i++ {
		AddEnumToSchema(opts, enums.Get(i), doc)
	}

	// Messages can have messages
	messages := md.Messages()
	for i := 0; i < messages.Len(); i++ {
		AddMessageSchemas(opts, messages.Get(i), doc)
	}
}

func AddFieldToSchema(opts options.Options, fd protoreflect.FieldDescriptor, doc *v3.Document) {
	if fd == nil {
		return
	}
	AddEnumToSchema(opts, fd.Enum(), doc)
	AddMessageSchemas(opts, fd.Message(), doc)
	AddFieldToSchema(opts, fd.MapKey(), doc)
	AddFieldToSchema(opts, fd.MapValue(), doc)
}

func AddEnumToSchema(opts options.Options, ed protoreflect.EnumDescriptor, doc *v3.Document) {
	if ed == nil {
		return
	}
	if visibility.ShouldBeFiltered(visibility.GetVisibilityRule(ed), opts.AllowedVisibilities) {
		opts.Logger.Debug("Filtering enum due to visibility", slog.String("enum", string(ed.FullName())), slog.Any("restriction_selectors", opts.AllowedVisibilities))
		return
	}
	if _, ok := doc.Components.Schemas.Get(string(ed.FullName())); ok {
		return
	}
	name, schema := enumToSchema(opts, ed)
	if schema != nil {
		doc.Components.Schemas.Set(name, base.CreateSchemaProxy(schema))
	}
}

func enumToSchema(opts options.Options, tt protoreflect.EnumDescriptor) (string, *base.Schema) {
	opts.Logger.Debug("enumToSchema", slog.Any("descriptor", tt.FullName()))
	children := []*yaml.Node{}
	// One description for each entry of the enum, in its order: a value's number, when it is
	// included, is described as its name is.
	descriptions := []*yaml.Node{}
	var listed strings.Builder
	locations := tt.ParentFile().SourceLocations()
	values := tt.Values()
	for i := 0; i < values.Len(); i++ {
		value := values.Get(i)
		if visibility.ShouldBeFiltered(visibility.GetVisibilityRule(value), opts.AllowedVisibilities) {
			opts.Logger.Debug("Filtering enum value due to visibility", slog.String("enum_value", string(value.FullName())), slog.Any("restriction_selectors", opts.AllowedVisibilities))
			continue // Skip this enum value
		}
		comment := util.FormatComments(locations.ByDescriptor(value))
		children = append(children, utils.CreateStringNode(string(value.Name())))
		descriptions = append(descriptions, utils.CreateStringNode(comment))
		if opts.IncludeNumberEnumValues {
			children = append(children, utils.CreateIntNode(strconv.FormatInt(int64(value.Number()), 10)))
			descriptions = append(descriptions, utils.CreateStringNode(comment))
		}
		if comment != "" {
			// A comment of several lines stays inside its list item: each line after the first is
			// indented under it, or Markdown would end the list there.
			lines := strings.Split(comment, "\n")
			for j := 1; j < len(lines); j++ {
				lines[j] = strings.TrimRight("  "+strings.TrimSpace(lines[j]), " ")
			}
			fmt.Fprintf(&listed, "\n- `%s`: %s", value.Name(), strings.Join(lines, "\n"))
		}
	}

	title := string(tt.Name())
	if opts.FullyQualifiedMessageNames {
		title = string(tt.FullName())
	}
	types := []string{"string"}
	if opts.IncludeNumberEnumValues {
		types = append(types, "number")
	}
	s := &base.Schema{
		Title:       title,
		Description: util.FormatComments(locations.ByDescriptor(tt)),
		Type:        types,
		Enum:        children,
	}
	if opts.WithEnumValueDescriptions && listed.Len() > 0 {
		// The list, for a reader of the description in any tool; the extension, for a tool
		// that pairs each value with its own text (openapi-generator's x-enum-descriptions).
		s.Description = strings.TrimSpace(s.Description + "\n" + listed.String())
		s.Extensions = orderedmap.New[string, *yaml.Node]()
		s.Extensions.Set("x-enum-descriptions", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: descriptions})
	}
	return string(tt.FullName()), s
}
