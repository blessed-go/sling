package config

import (
	"fmt"
	"reflect"
	"strings"
)

// Defaulter enables a configuration struct to set dynamic defaults or normalize values.
type Defaulter interface {
	SetDefaults()
}

// Validator enables a configuration struct to validate invariants post-loading.
type Validator interface {
	Validate() error
}

// SetDefaultsRecursive traverses the configuration tree and invokes SetDefaults on all applicable nodes.
func SetDefaultsRecursive(cfg any) error {
	val := reflect.ValueOf(cfg)
	applyDefaultsNode(val)
	return nil
}

func applyDefaultsNode(v reflect.Value) {
	if !v.IsValid() {
		return
	}

	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		if defaulter, ok := v.Interface().(Defaulter); ok {
			defaulter.SetDefaults()
		}
		applyDefaultsNode(v.Elem())
		return
	}

	if v.Kind() == reflect.Struct {
		if v.CanAddr() {
			if defaulter, ok := v.Addr().Interface().(Defaulter); ok {
				defaulter.SetDefaults()
			}
		} else if defaulter, ok := v.Interface().(Defaulter); ok {
			defaulter.SetDefaults()
		}

		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			applyDefaultsNode(v.Field(i))
		}
		return
	}

	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		for i := 0; i < v.Len(); i++ {
			applyDefaultsNode(v.Index(i))
		}
		return
	}

	if v.Kind() == reflect.Map {
		for _, key := range v.MapKeys() {
			elem := v.MapIndex(key)
			if elem.Kind() == reflect.Ptr {
				applyDefaultsNode(elem)
			} else if elem.Kind() == reflect.Struct {
				// Maps in Go are not addressable: make a copy, apply defaults, and store back.
				copyVal := reflect.New(elem.Type()).Elem()
				copyVal.Set(elem)
				applyDefaultsNode(copyVal.Addr())
				v.SetMapIndex(key, copyVal)
			}
		}
		return
	}
}

// ValidateRecursive traverses the configuration tree and validates all nodes implementing Validator.
func ValidateRecursive(cfg any) error {
	val := reflect.ValueOf(cfg)
	return validateNode(val, "")
}

func validateNode(v reflect.Value, path string) error {
	if !v.IsValid() {
		return nil
	}

	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		if validator, ok := v.Interface().(Validator); ok {
			if err := validator.Validate(); err != nil {
				return formatValidationError(path, err)
			}
		}
		return validateNode(v.Elem(), path)
	}

	if v.Kind() == reflect.Struct {
		var validator Validator
		var ok bool

		if v.CanAddr() {
			validator, ok = v.Addr().Interface().(Validator)
		}
		if !ok {
			validator, ok = v.Interface().(Validator)
		}

		if ok {
			if err := validator.Validate(); err != nil {
				return formatValidationError(path, err)
			}
		}

		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}

			fieldVal := v.Field(i)
			fieldName := strings.Split(field.Tag.Get("toml"), ",")[0]
			if fieldName == "" || fieldName == "-" {
				fieldName = field.Name
			}

			fieldPath := fieldName
			if field.Anonymous {
				fieldPath = path
			} else if path != "" {
				fieldPath = path + "." + fieldName
			}

			if err := validateNode(fieldVal, fieldPath); err != nil {
				return err
			}
		}
		return nil
	}

	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		for i := 0; i < v.Len(); i++ {
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := validateNode(v.Index(i), elemPath); err != nil {
				return err
			}
		}
		return nil
	}

	if v.Kind() == reflect.Map {
		for _, key := range v.MapKeys() {
			elemPath := fmt.Sprintf("%s[%v]", path, key.Interface())
			if err := validateNode(v.MapIndex(key), elemPath); err != nil {
				return err
			}
		}
		return nil
	}

	return nil
}

func formatValidationError(path string, err error) error {
	if path == "" {
		return fmt.Errorf("config validation failed: %w", err)
	}
	return fmt.Errorf("config validation failed at %q: %w", path, err)
}
