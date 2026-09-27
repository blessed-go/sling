package config

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2/unstable/edit"
)

func ParseDefaultToType(val string, kind reflect.Kind, elemKind reflect.Kind) any {
	if val == "" {
		return GetZeroValueByType(kind)
	}
	switch kind {
	case reflect.Bool:
		if b, err := strconv.ParseBool(val); err == nil {
			return b
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u, err := strconv.ParseUint(val, 10, 64); err == nil {
			return u
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	case reflect.Slice:
		var parts []string
		r := csv.NewReader(strings.NewReader(val))
		r.Comma = ','
		r.TrimLeadingSpace = true
		records, err := r.Read()
		if err == nil {
			parts = records
		} else {
			parts = strings.Split(val, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
		}

		res := make([]any, len(parts))
		for i, p := range parts {
			if elemKind == reflect.String {
				res[i] = p
				continue
			}

			if valInt, err := strconv.ParseInt(p, 10, 64); err == nil && elemKind != reflect.Bool {
				res[i] = valInt
			} else if valBool, err := strconv.ParseBool(p); err == nil {
				res[i] = valBool
			} else if valDur, err := time.ParseDuration(p); err == nil {
				res[i] = valDur
			} else if valTime, err := time.Parse(time.RFC3339, p); err == nil {
				res[i] = valTime
			} else if valFloat, err := strconv.ParseFloat(p, 64); err == nil {
				res[i] = valFloat
			} else {
				res[i] = p
			}
		}
		return res
	}
	return val
}

func GetZeroValueByType(kind reflect.Kind) any {
	switch kind {
	case reflect.Bool:
		return false
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int64(0)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return uint64(0)
	case reflect.Float32, reflect.Float64:
		return float64(0.0)
	case reflect.Slice:
		return []any{}
	case reflect.Map:
		return map[string]any{}
	default:
		return ""
	}
}

func RepairTOMLFile(path string, schema *SchemaNode) (bool, error) {
	fileBytes, err := os.ReadFile(path)
	isNotExist := errors.Is(err, fs.ErrNotExist)

	if err != nil && !isNotExist {
		return false, fmt.Errorf("failed to read existing config: %w", err)
	}

	if isNotExist {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return false, fmt.Errorf("failed to create config dir: %w", err)
		}
		fileBytes = []byte{}
	}

	doc, err := edit.Parse(fileBytes)
	if err != nil {
		return false, fmt.Errorf("invalid TOML syntax in configuration file: %w", err)
	}

	patched := false
	var applySchema func(node *SchemaNode, pathAcc []string) error
	applySchema = func(node *SchemaNode, pathAcc []string) error {
		for _, field := range node.Fields {
			fieldPath := make([]string, len(pathAcc)+1)
			copy(fieldPath, pathAcc)
			fieldPath[len(pathAcc)] = field.Key

			_, ok := doc.Get(fieldPath)
			if !ok {
				if field.IsRequired && field.DefaultVal == "" {
					continue
				}

				valToSet := ParseDefaultToType(field.DefaultVal, field.Kind, field.ElemKind)

				if err := doc.Set(fieldPath, valToSet); err != nil {
					return fmt.Errorf("failed to set missing field %v: %w", fieldPath, err)
				}
				if field.Comment != "" {
					doc.SetComment(fieldPath, field.Comment)
				}
				patched = true
			}
		}

		for key, child := range node.Children {
			childPath := make([]string, len(pathAcc)+1)
			copy(childPath, pathAcc)
			childPath[len(pathAcc)] = key

			if err := applySchema(child, childPath); err != nil {
				return err
			}
		}
		return nil
	}

	if err := applySchema(schema, []string{}); err != nil {
		return false, err
	}

	if patched || isNotExist {
		updatedBytes := doc.Bytes()
		// Preserve inode via truncate and write to ensure volume mounts and file watchers remain intact.
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return false, fmt.Errorf("failed to open config file for writing: %w", err)
		}
		defer file.Close()

		if _, err := file.Write(updatedBytes); err != nil {
			return false, fmt.Errorf("failed to write config file: %w", err)
		}
	}

	return patched, nil
}
