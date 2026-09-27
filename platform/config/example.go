package config

import (
	"fmt"
	"strings"
)

func GenerateExample(schema *SchemaNode) string {
	var sb strings.Builder
	sb.WriteString("# Automatically generated configuration template\n")

	var traverse func(node *SchemaNode, indent string)
	traverse = func(node *SchemaNode, indent string) {
		if node.Section != "" {
			sb.WriteString(fmt.Sprintf("\n%s# [%s]\n", indent, node.Section))
		}

		for _, field := range node.Fields {
			if field.Comment != "" {
				sb.WriteString(fmt.Sprintf("%s# # %s\n", indent, field.Comment))
			}
			val := field.DefaultVal
			if val == "" {
				val = GetZeroValueString(field.Kind)
			}
			sb.WriteString(fmt.Sprintf("%s# %s = %s\n", indent, field.Key, val))
		}

		for _, child := range node.Children {
			traverse(child, indent+"  ")
		}
	}

	traverse(schema, "")
	return sb.String()
}
