package config

import (
	"fmt"
	"reflect"
	"strings"
)

type FallbackResolver struct {
	root     reflect.Value
	visited  map[string]bool
	visiting map[string]bool
}

func NewFallbackResolver(root reflect.Value) (*FallbackResolver, error) {
	if root.Kind() != reflect.Ptr || root.IsNil() {
		return nil, fmt.Errorf("root must be a non-nil pointer to struct")
	}
	return &FallbackResolver{
		root:     root.Elem(),
		visited:  make(map[string]bool),
		visiting: make(map[string]bool),
	}, nil
}

func (r *FallbackResolver) Resolve() error {
	return r.resolveValue("", r.root)
}

func (r *FallbackResolver) resolveValue(prefix string, current reflect.Value) error {
	if current.Kind() == reflect.Ptr {
		if current.IsNil() {
			return nil
		}
		current = current.Elem()
	}

	if current.Kind() == reflect.Slice || current.Kind() == reflect.Array {
		for i := 0; i < current.Len(); i++ {
			elemPath := fmt.Sprintf("%s[%d]", prefix, i)
			if err := r.resolveValue(elemPath, current.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}

	if current.Kind() == reflect.Map {
		for _, key := range current.MapKeys() {
			val := current.MapIndex(key)
			elemPath := fmt.Sprintf("%s[%v]", prefix, key.Interface())

			if val.Kind() == reflect.Struct {
				// Maps in Go are not addressable. Copy the struct, apply fallbacks, and write back.
				copyVal := reflect.New(val.Type()).Elem()
				copyVal.Set(val)
				if err := r.resolveValue(elemPath, copyVal); err != nil {
					return err
				}
				current.SetMapIndex(key, copyVal)
			} else {
				if err := r.resolveValue(elemPath, val); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if current.Kind() != reflect.Struct {
		return nil
	}

	t := current.Type()
	for i := 0; i < current.NumField(); i++ {
		fieldVal := current.Field(i)
		fieldType := t.Field(i)
		if fieldType.PkgPath != "" {
			continue // Skip unexported fields
		}

		// Handle embedded struct traversal.
		fieldTypeActual := fieldType.Type
		if fieldTypeActual.Kind() == reflect.Ptr {
			fieldTypeActual = fieldTypeActual.Elem()
		}
		if fieldType.Anonymous && fieldTypeActual.Kind() == reflect.Struct {
			if err := r.resolveValue(prefix, fieldVal); err != nil {
				return err
			}
			continue
		}

		tomlTag := strings.Split(fieldType.Tag.Get("toml"), ",")[0]
		if tomlTag == "" {
			tomlTag = fieldType.Name
		}

		fieldPath := tomlTag
		if prefix != "" {
			fieldPath = prefix + "." + tomlTag
		}

		fallbackPath := fieldType.Tag.Get("fallback")
		if fallbackPath != "" {
			if err := r.resolveFieldWithPath(fieldPath, fieldVal, fallbackPath); err != nil {
				return err
			}
		}

		if fieldVal.Kind() == reflect.Ptr && fieldVal.IsNil() {
			continue
		}
		if err := r.resolveValue(fieldPath, fieldVal); err != nil {
			return err
		}
	}
	return nil
}

func (r *FallbackResolver) resolveFieldWithPath(fieldPath string, fieldVal reflect.Value, fallbackPath string) error {
	if r.visited[fieldPath] {
		return nil
	}
	if r.visiting[fieldPath] {
		return fmt.Errorf("circular fallback dependency detected at path: %s", fieldPath)
	}

	r.visiting[fieldPath] = true
	defer func() {
		r.visiting[fieldPath] = false
	}()

	// Retrieve source field by path.
	srcVal, srcField, exists := getFieldAndTypeByPath(r.root, fallbackPath)
	if !exists {
		return fmt.Errorf("fallback source path %q for field %q does not exist", fallbackPath, fieldPath)
	}

	// Resolve source dependencies recursively (DAG).
	srcFallback := srcField.Tag.Get("fallback")
	if srcFallback != "" && !r.visited[fallbackPath] {
		if err := r.resolveFieldWithPath(fallbackPath, srcVal, srcFallback); err != nil {
			return err
		}
	}

	// Compare dereferenced value types.
	targetVal := fieldVal
	if targetVal.Kind() == reflect.Ptr {
		if !targetVal.IsNil() {
			targetVal = targetVal.Elem()
		}
	}

	sourceVal := srcVal
	if sourceVal.Kind() == reflect.Ptr {
		if sourceVal.IsNil() {
			return nil // Source is empty; fallback cannot be applied.
		}
		sourceVal = sourceVal.Elem()
	}

	destType := targetVal.Type()
	if fieldVal.Kind() == reflect.Ptr {
		destType = fieldVal.Type().Elem()
	}
	srcType := sourceVal.Type()

	if destType != srcType && !srcType.ConvertibleTo(destType) {
		return fmt.Errorf("fallback type mismatch: cannot convert %s to %s for path %s", srcType, destType, fieldPath)
	}

	dstKind := targetVal.Kind()
	srcKind := sourceVal.Kind()

	if dstKind == reflect.Struct && srcKind == reflect.Struct {
		// Perform deep field merge for structs.
		mergeStructs(fieldVal, srcVal)
	} else {
		if isZeroValue(targetVal) && !isZeroValue(sourceVal) && fieldVal.CanSet() {
			if fieldVal.Kind() == reflect.Ptr && fieldVal.IsNil() {
				fieldVal.Set(reflect.New(fieldVal.Type().Elem()))
				targetVal = fieldVal.Elem()
			}
			if srcType.ConvertibleTo(destType) {
				targetVal.Set(sourceVal.Convert(destType))
			} else {
				targetVal.Set(sourceVal)
			}
		}
	}

	r.visited[fieldPath] = true
	return nil
}

func mergeStructs(dst, src reflect.Value) {
	if dst.Kind() == reflect.Ptr {
		if dst.IsNil() {
			if !src.IsNil() {
				dst.Set(reflect.New(dst.Type().Elem()))
			} else {
				return
			}
		}
		dst = dst.Elem()
	}
	if src.Kind() == reflect.Ptr {
		if src.IsNil() {
			return
		}
		src = src.Elem()
	}

	if dst.Kind() != reflect.Struct || src.Kind() != reflect.Struct {
		return
	}

	t := dst.Type()
	for i := 0; i < dst.NumField(); i++ {
		dstField := dst.Field(i)
		structField := t.Field(i)
		if structField.PkgPath != "" {
			continue
		}

		structFieldActual := structField.Type
		if structFieldActual.Kind() == reflect.Ptr {
			structFieldActual = structFieldActual.Elem()
		}
		if structField.Anonymous && structFieldActual.Kind() == reflect.Struct {
			mergeStructs(dstField, src)
			continue
		}

		srcField := src.FieldByName(structField.Name)
		if !srcField.IsValid() {
			continue
		}

		if dstField.CanSet() {
			if isZeroValue(dstField) && !isZeroValue(srcField) {
				if dstField.Kind() == reflect.Ptr && dstField.IsNil() {
					dstField.Set(reflect.New(dstField.Type().Elem()))
				}
				targetDst := dstField
				if targetDst.Kind() == reflect.Ptr {
					targetDst = targetDst.Elem()
				}
				targetSrc := srcField
				if targetSrc.Kind() == reflect.Ptr {
					targetSrc = targetSrc.Elem()
				}

				if targetSrc.Type().AssignableTo(targetDst.Type()) {
					targetDst.Set(targetSrc)
				} else if targetSrc.Type().ConvertibleTo(targetDst.Type()) {
					targetDst.Set(targetSrc.Convert(targetDst.Type()))
				}
			} else {
				dstSubKind := dstField.Kind()
				if dstSubKind == reflect.Ptr && !dstField.IsNil() {
					dstSubKind = dstField.Elem().Kind()
				}
				srcSubKind := srcField.Kind()
				if srcSubKind == reflect.Ptr && !srcField.IsNil() {
					srcSubKind = srcField.Elem().Kind()
				}

				if dstSubKind == reflect.Struct && srcSubKind == reflect.Struct {
					mergeStructs(dstField, srcField)
				}
			}
		}
	}
}

// findFieldInStruct recursively searches for a field, including nested anonymous (promoted) structs.
func findFieldInStruct(current reflect.Value, part string) (reflect.Value, reflect.StructField, bool) {
	if current.Kind() == reflect.Ptr {
		if current.IsNil() {
			return reflect.Value{}, reflect.StructField{}, false
		}
		current = current.Elem()
	}
	if current.Kind() != reflect.Struct {
		return reflect.Value{}, reflect.StructField{}, false
	}

	t := current.Type()
	for i := 0; i < current.NumField(); i++ {
		field := t.Field(i)
		tomlTag := strings.Split(field.Tag.Get("toml"), ",")[0]
		if tomlTag == part || strings.EqualFold(field.Name, part) {
			return current.Field(i), field, true
		}
	}

	for i := 0; i < current.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous {
			subVal, subField, found := findFieldInStruct(current.Field(i), part)
			if found {
				return subVal, subField, true
			}
		}
	}

	return reflect.Value{}, reflect.StructField{}, false
}

func getFieldAndTypeByPath(root reflect.Value, path string) (reflect.Value, reflect.StructField, bool) {
	parts := strings.Split(path, ".")
	current := root
	var lastField reflect.StructField
	var exists bool

	for _, part := range parts {
		current, lastField, exists = findFieldInStruct(current, part)
		if !exists {
			return reflect.Value{}, reflect.StructField{}, false
		}
	}
	return current, lastField, true
}

func isZeroValue(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	case reflect.Ptr:
		return v.IsNil()
	default:
		return v.IsZero()
	}
}
