package schema

import (
	"strings"
	"testing"
)

func TestValidateIssuesJSON_Valid(t *testing.T) {
	json := `{
		"issues": [{
			"severity": "high",
			"file": "auth.go",
			"title": "SQL injection",
			"description": "Input not sanitized"
		}]
	}`
	result := ValidateIssuesJSON(json)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidateIssuesJSON_AllFields(t *testing.T) {
	json := `{
		"issues": [{
			"severity": "critical",
			"category": "security",
			"file": "auth.go",
			"line": 42,
			"endLine": 50,
			"title": "SQL injection",
			"description": "Input not sanitized",
			"suggestedFix": "Use parameterized queries",
			"codeSnippet": "query := fmt.Sprintf(sql, input)",
			"raisedBy": ["reviewer-1", "reviewer-2"]
		}]
	}`
	result := ValidateIssuesJSON(json)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidateIssuesJSON_EmptyIssues(t *testing.T) {
	json := `{"issues": []}`
	result := ValidateIssuesJSON(json)
	if !result.Valid {
		t.Errorf("expected valid for empty issues array, got errors: %v", result.Errors)
	}
}

func TestValidateIssuesJSON_InvalidSeverity(t *testing.T) {
	json := `{
		"issues": [{
			"severity": "urgent",
			"file": "auth.go",
			"title": "Bug",
			"description": "desc"
		}]
	}`
	result := ValidateIssuesJSON(json)
	if result.Valid {
		t.Error("expected invalid for bad severity")
	}
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Description, "must be one of") || strings.Contains(e.Field, "severity") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected severity error, got: %v", result.Errors)
	}
}

func TestValidateIssuesJSON_MissingRequired(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"missing file", `{"issues": [{"severity": "high", "title": "T", "description": "D"}]}`},
		{"missing title", `{"issues": [{"severity": "high", "file": "a.go", "description": "D"}]}`},
		{"missing description", `{"issues": [{"severity": "high", "file": "a.go", "title": "T"}]}`},
		{"missing severity", `{"issues": [{"file": "a.go", "title": "T", "description": "D"}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateIssuesJSON(tt.json)
			if result.Valid {
				t.Errorf("expected invalid for %s", tt.name)
			}
		})
	}
}

func TestValidateIssuesJSON_EmptyFile(t *testing.T) {
	json := `{
		"issues": [{
			"severity": "high",
			"file": "",
			"title": "Bug",
			"description": "desc"
		}]
	}`
	result := ValidateIssuesJSON(json)
	if result.Valid {
		t.Error("expected invalid for empty file")
	}
}

func TestValidateIssuesJSON_AnyCategory(t *testing.T) {
	// category is a free-form string, no enum constraint
	json := `{
		"issues": [{
			"severity": "high",
			"category": "reliability",
			"file": "a.go",
			"title": "Bug",
			"description": "desc"
		}]
	}`
	result := ValidateIssuesJSON(json)
	if !result.Valid {
		t.Errorf("expected valid for any category string, got errors: %v", result.Errors)
	}
}

func TestValidateIssuesJSON_InvalidLine(t *testing.T) {
	json := `{
		"issues": [{
			"severity": "high",
			"file": "a.go",
			"line": 0,
			"title": "Bug",
			"description": "desc"
		}]
	}`
	result := ValidateIssuesJSON(json)
	if result.Valid {
		t.Error("expected invalid for line=0")
	}
}

func TestValidateIssuesJSON_InvalidJSON(t *testing.T) {
	result := ValidateIssuesJSON("{not valid json}")
	if result.Valid {
		t.Error("expected invalid for bad JSON")
	}
}

func TestValidateIssuesJSON_MissingIssuesKey(t *testing.T) {
	result := ValidateIssuesJSON(`{"data": []}`)
	if result.Valid {
		t.Error("expected invalid when missing issues key")
	}
}

func TestFormatErrorsForRetry(t *testing.T) {
	vr := &ValidationResult{
		Valid: false,
		Errors: []ValidationError{
			{Field: "issues.0.severity", Description: "must be one of: critical, high, medium, low, nitpick"},
			{Field: "issues.2.file", Description: "required field is missing"},
		},
	}
	result := FormatErrorsForRetry(vr)
	if !strings.Contains(result, "issues.0.severity") {
		t.Error("missing severity error")
	}
	if !strings.Contains(result, "issues.2.file") {
		t.Error("missing file error")
	}
}

func TestFormatErrorsForRetry_Nil(t *testing.T) {
	if FormatErrorsForRetry(nil) != "" {
		t.Error("expected empty string for nil")
	}
	if FormatErrorsForRetry(&ValidationResult{}) != "" {
		t.Error("expected empty string for no errors")
	}
}

func TestGetSchemaString(t *testing.T) {
	s := GetSchemaString("issues")
	if !strings.Contains(s, "json-schema.org") {
		t.Error("expected schema content")
	}
	if !strings.Contains(s, "severity") {
		t.Error("expected severity in schema")
	}
}

func TestGetSchemaString_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown schema")
		}
	}()
	GetSchemaString("nonexistent")
}

func TestValidateIssuesJSON_AllSeverities(t *testing.T) {
	severities := []string{"critical", "high", "medium", "low", "nitpick"}
	for _, sev := range severities {
		json := `{"issues": [{"severity": "` + sev + `", "file": "a.go", "title": "T", "description": "D"}]}`
		result := ValidateIssuesJSON(json)
		if !result.Valid {
			t.Errorf("severity %q should be valid, got errors: %v", sev, result.Errors)
		}
	}
}

func TestValidateIssuesJSON_MixedValidAndInvalid(t *testing.T) {
	// Schema validates the whole document - if any issue is invalid, the whole thing is invalid
	json := `{
		"issues": [
			{"severity": "high", "file": "a.go", "title": "Valid", "description": "Good"},
			{"severity": "invalid", "file": "b.go", "title": "Bad", "description": "Bad sev"}
		]
	}`
	result := ValidateIssuesJSON(json)
	if result.Valid {
		t.Error("expected invalid when any issue has bad severity")
	}
}

func TestValidateJSON_IssuesDelta_Valid(t *testing.T) {
	json := `{
		"add": [{"severity":"high","file":"a.go","title":"T","description":"D"}],
		"retract": ["I1"],
		"update": [{"id":"I2","severity":"medium","description":"updated"}],
		"support": [{"issueRef":"claude:I3"}],
		"withdraw": [{"issueRef":"gpt4o:I4"}],
		"contest": [{"issueRef":"codex:I5"}]
	}`
	result := ValidateJSON("issues_delta", json)
	if !result.Valid {
		t.Errorf("expected valid issues_delta JSON, got errors: %v", result.Errors)
	}
}

func TestValidateJSON_IssuesDelta_Invalid(t *testing.T) {
	json := `{"add":[{"file":"a.go","title":"T","description":"D"}],"retract":[],"update":[],"support":[],"withdraw":[],"contest":[]}`
	result := ValidateJSON("issues_delta", json)
	if result.Valid {
		t.Error("expected invalid issues_delta JSON when severity is missing")
	}
}

func TestValidateJSON_IssuesDelta_MissingCanonicalKeys(t *testing.T) {
	json := `{"add":[],"retract":[],"update":[]}`
	result := ValidateJSON("issues_delta", json)
	if result.Valid {
		t.Error("expected invalid issues_delta JSON when support/withdraw/contest keys are missing")
	}
}
