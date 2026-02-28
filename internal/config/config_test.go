package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	yamlContent := `
defaults:
  max_rounds: 3
  output_format: markdown
  check_convergence: true
reviewers:
  senior:
    model: claude-code
    prompt: "Review this code carefully."
  junior:
    model: mock
    prompt: "Quick review."
analyzer:
  model: claude-code
  prompt: "Analyze the code."
summarizer:
  model: gpt-4o
  prompt: "Summarize findings."
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.Defaults.MaxRounds != 3 {
		t.Errorf("MaxRounds = %d, want 3", cfg.Defaults.MaxRounds)
	}
	if cfg.Defaults.OutputFormat != "markdown" {
		t.Errorf("OutputFormat = %q, want %q", cfg.Defaults.OutputFormat, "markdown")
	}
	if !cfg.Defaults.CheckConvergence {
		t.Error("CheckConvergence = false, want true")
	}
	if cfg.Defaults.StructurizeMode != "ledger" {
		t.Errorf("StructurizeMode = %q, want %q", cfg.Defaults.StructurizeMode, "ledger")
	}
	if len(cfg.Reviewers) != 2 {
		t.Errorf("len(Reviewers) = %d, want 2", len(cfg.Reviewers))
	}
	if cfg.Reviewers["senior"].Model != "claude-code" {
		t.Errorf("Reviewers[senior].Model = %q, want %q", cfg.Reviewers["senior"].Model, "claude-code")
	}
	if cfg.Analyzer.Model != "claude-code" {
		t.Errorf("Analyzer.Model = %q, want %q", cfg.Analyzer.Model, "claude-code")
	}
	if cfg.Summarizer.Model != "gpt-4o" {
		t.Errorf("Summarizer.Model = %q, want %q", cfg.Summarizer.Model, "gpt-4o")
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	t.Setenv("CODEDEBATE_TEST_MODEL", "expanded-model")
	t.Setenv("CODEDEBATE_TEST_PROMPT", "expanded prompt text")

	yamlContent := `
defaults:
  max_rounds: 1
reviewers:
  r1:
    model: "${CODEDEBATE_TEST_MODEL}"
    prompt: "${CODEDEBATE_TEST_PROMPT}"
analyzer:
  model: "${CODEDEBATE_TEST_MODEL}"
  prompt: "analyze"
summarizer:
  model: gpt-4o
  prompt: "summarize"
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.Reviewers["r1"].Model != "expanded-model" {
		t.Errorf("Reviewers[r1].Model = %q, want %q", cfg.Reviewers["r1"].Model, "expanded-model")
	}
	if cfg.Reviewers["r1"].Prompt != "expanded prompt text" {
		t.Errorf("Reviewers[r1].Prompt = %q, want %q", cfg.Reviewers["r1"].Prompt, "expanded prompt text")
	}
	if cfg.Analyzer.Model != "expanded-model" {
		t.Errorf("Analyzer.Model = %q, want %q", cfg.Analyzer.Model, "expanded-model")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	badYAML := `
defaults:
  max_rounds: [invalid
  broken: yaml: content
`
	if err := os.WriteFile(cfgPath, []byte(badYAML), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CodeDebateConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "missing reviewers",
			cfg: CodeDebateConfig{
				Defaults:   DefaultsConfig{MaxRounds: 1},
				Reviewers:  map[string]ReviewerConfig{},
				Analyzer:   ReviewerConfig{Model: "m", Prompt: "p"},
				Summarizer: ReviewerConfig{Model: "gpt-4o", Prompt: "p"},
			},
			wantErr: true,
			errMsg:  "at least one reviewer",
		},
		{
			name: "missing analyzer model",
			cfg: CodeDebateConfig{
				Defaults:   DefaultsConfig{MaxRounds: 1},
				Reviewers:  map[string]ReviewerConfig{"r1": {Model: "m", Prompt: "p"}},
				Analyzer:   ReviewerConfig{Model: "", Prompt: "p"},
				Summarizer: ReviewerConfig{Model: "gpt-4o", Prompt: "p"},
			},
			wantErr: true,
			errMsg:  "analyzer is missing a \"model\" field",
		},
		{
			name: "missing summarizer prompt",
			cfg: CodeDebateConfig{
				Defaults:   DefaultsConfig{MaxRounds: 1},
				Reviewers:  map[string]ReviewerConfig{"r1": {Model: "m", Prompt: "p"}},
				Analyzer:   ReviewerConfig{Model: "m", Prompt: "p"},
				Summarizer: ReviewerConfig{Model: "gpt-4o", Prompt: ""},
			},
			wantErr: true,
			errMsg:  "summarizer is missing a \"prompt\" field",
		},
		{
			name: "max_rounds zero",
			cfg: CodeDebateConfig{
				Defaults:   DefaultsConfig{MaxRounds: 0},
				Reviewers:  map[string]ReviewerConfig{"r1": {Model: "m", Prompt: "p"}},
				Analyzer:   ReviewerConfig{Model: "m", Prompt: "p"},
				Summarizer: ReviewerConfig{Model: "gpt-4o", Prompt: "p"},
			},
			wantErr: true,
			errMsg:  "max_rounds must be > 0",
		},
		{
			name: "summarizer must be openai model",
			cfg: CodeDebateConfig{
				Defaults:   DefaultsConfig{MaxRounds: 2},
				Reviewers:  map[string]ReviewerConfig{"r1": {Model: "m", Prompt: "p"}},
				Analyzer:   ReviewerConfig{Model: "m", Prompt: "p"},
				Summarizer: ReviewerConfig{Model: "claude-code", Prompt: "p"},
			},
			wantErr: true,
			errMsg:  "summarizer.model must be an API model",
		},
		{
			name: "valid config",
			cfg: CodeDebateConfig{
				Defaults:   DefaultsConfig{MaxRounds: 2, StructurizeMode: "ledger"},
				Reviewers:  map[string]ReviewerConfig{"r1": {Model: "m", Prompt: "p"}},
				Analyzer:   ReviewerConfig{Model: "m", Prompt: "p"},
				Summarizer: ReviewerConfig{Model: "gpt-4o", Prompt: "p"},
			},
			wantErr: false,
		},
		{
			name: "invalid structurize mode",
			cfg: CodeDebateConfig{
				Defaults:   DefaultsConfig{MaxRounds: 2, StructurizeMode: "unknown"},
				Reviewers:  map[string]ReviewerConfig{"r1": {Model: "m", Prompt: "p"}},
				Analyzer:   ReviewerConfig{Model: "m", Prompt: "p"},
				Summarizer: ReviewerConfig{Model: "gpt-4o", Prompt: "p"},
			},
			wantErr: true,
			errMsg:  "defaults.structurize_mode must be one of: legacy, ledger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(&tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				if tt.errMsg != "" && !containsStr(err.Error(), tt.errMsg) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
