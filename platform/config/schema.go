package config

import (
	"fmt"
	"reflect"
	"strings"
)

// SchemaField represents metadata for a single configuration field.
type SchemaField struct {
	Path       string
	Key        string
	DefaultVal string
	Comment    string
	Fallback   string
	IsRequired bool
	Kind       reflect.Kind
	ElemKind   reflect.Kind
}

// SchemaNode represents a hierarchical node within the configuration structure.
type SchemaNode struct {
	Section  string
	Fields   []SchemaField
	Children map[string]*SchemaNode
}

// BuildSchema inspects a struct type and constructs a SchemaNode tree.
func BuildSchema(t reflect.Type, currentSection string) (*SchemaNode, error) {
	if t == nil {
		return nil, fmt.Errorf("config: schema target type cannot be nil")
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, nil
	}

	node := &SchemaNode{
		Section:  currentSection,
		Fields:   make([]SchemaField, 0),
		Children: make(map[string]*SchemaNode),
	}

	if err := parseStructToNode(t, currentSection, node); err != nil {
		return nil, err
	}

	return node, nil
}

func parseStructToNode(t reflect.Type, currentSection string, node *SchemaNode) error {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}

		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		if field.Anonymous && fieldType.Kind() == reflect.Struct {
			if err := parseStructToNode(fieldType, currentSection, node); err != nil {
				return err
			}
			continue
		}

		tomlTag := field.Tag.Get("toml")
		if tomlTag == "-" {
			continue
		}
		key := tomlTag
		if key == "" {
			key = field.Name
		} else {
			key = strings.Split(tomlTag, ",")[0]
		}

		fullPath := key
		if currentSection != "" {
			fullPath = currentSection + "." + key
		}

		comment := field.Tag.Get("comment")
		fallback := field.Tag.Get("fallback")
		envDefault := field.Tag.Get("env-default")
		if envDefault == "" {
			envDefault = field.Tag.Get("envDefault")
		}
		required := field.Tag.Get("env-required") == "true"

		if envDefault != "" && fallback != "" {
			return fmt.Errorf("field %s: mutual exclusivity violation: cannot define both env-default and fallback", fullPath)
		}

		if fieldType.Kind() == reflect.Struct {
			childNode, err := BuildSchema(fieldType, fullPath)
			if err != nil {
				return err
			}
			if childNode != nil {
				node.Children[key] = childNode
			}
		} else {
			// Determine slice element kind.
			var elemKind reflect.Kind
			if fieldType.Kind() == reflect.Slice {
				elemKind = fieldType.Elem().Kind()
				if elemKind == reflect.Ptr {
					elemKind = fieldType.Elem().Elem().Kind()
				}
			}

			node.Fields = append(node.Fields, SchemaField{
				Path:       fullPath,
				Key:        key,
				DefaultVal: envDefault,
				Comment:    comment,
				Fallback:   fallback,
				IsRequired: required,
				Kind:       fieldType.Kind(),
				ElemKind:   elemKind,
			})
		}
	}
	return nil
}

func GetZeroValueString(kind reflect.Kind) string {
	switch kind {
	case reflect.Bool:
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "0"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "0"
	case reflect.Float32, reflect.Float64:
		return "0.0"
	case reflect.Slice:
		return "[]"
	case reflect.Map:
		return "{}"
	default:
		return "\"\""
	}
}
