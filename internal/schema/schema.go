package schema

import (
	"embed"
	"fmt"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// ValidationError 表示单个 schema 校验错误。
type ValidationError struct {
	Field       string // 如 "issues.0.severity"
	Description string // 如 "must be one of: critical, high, medium, low, nitpick"
	Value       any    // 实际值
}

// ValidationResult 表示 schema 校验结果。
type ValidationResult struct {
	Valid  bool
	Errors []ValidationError
}

// ValidateIssuesJSON 使用 JSON Schema 校验原始 JSON 字符串。
func ValidateIssuesJSON(jsonStr string) *ValidationResult {
	return ValidateJSON("issues", jsonStr)
}

// ValidateJSON 使用指定 schema 校验原始 JSON 字符串。
func ValidateJSON(schemaName, jsonStr string) *ValidationResult {
	schemaBytes, err := schemaFS.ReadFile(fmt.Sprintf("schemas/%s.json", schemaName))
	if err != nil {
		return &ValidationResult{
			Valid:  false,
			Errors: []ValidationError{{Field: "", Description: fmt.Sprintf("failed to load schema: %v", err)}},
		}
	}

	schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)
	documentLoader := gojsonschema.NewStringLoader(jsonStr)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return &ValidationResult{
			Valid:  false,
			Errors: []ValidationError{{Field: "", Description: fmt.Sprintf("validation error: %v", err)}},
		}
	}

	if result.Valid() {
		return &ValidationResult{Valid: true}
	}

	var errors []ValidationError
	for _, desc := range result.Errors() {
		errors = append(errors, ValidationError{
			Field:       desc.Field(),
			Description: desc.Description(),
			Value:       desc.Value(),
		})
	}

	return &ValidationResult{
		Valid:  false,
		Errors: errors,
	}
}

// FormatErrorsForRetry 将校验错误格式化为 AI 友好的重试提示。
func FormatErrorsForRetry(vr *ValidationResult) string {
	if vr == nil || len(vr.Errors) == 0 {
		return ""
	}

	var lines []string
	for _, e := range vr.Errors {
		if e.Field != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", e.Field, e.Description))
		} else {
			lines = append(lines, fmt.Sprintf("- %s", e.Description))
		}
	}
	return strings.Join(lines, "\n")
}

// GetSchemaString 获取 schema 原文，用于注入 prompt 模板。
func GetSchemaString(name string) string {
	data, err := schemaFS.ReadFile(fmt.Sprintf("schemas/%s.json", name))
	if err != nil {
		panic(fmt.Sprintf("schema %s not found: %v", name, err))
	}
	return string(data)
}
