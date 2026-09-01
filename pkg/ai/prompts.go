package ai

import (
	"embed"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var embeddedTemplates embed.FS

// TemplateConfig holds configuration for template loading and rendering
type TemplateConfig struct {
	// CustomTemplatePath allows overriding the template location
	CustomTemplatePath string

	// CustomVariables allows adding or overriding template variables
	CustomVariables map[string]any
}

// BuildPrompt constructs the AI prompt from a template using the suggestion request
func BuildPrompt(req *SuggestionRequest, config *TemplateConfig) (string, error) {
	if config == nil {
		config = &TemplateConfig{}
	}

	// Load the template
	tmplContent, err := loadTemplate("apply-suggestion.tmpl", config)
	if err != nil {
		return "", fmt.Errorf("failed to load template: %w", err)
	}

	// Create template with custom functions
	tmpl, err := template.New("prompt").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).Parse(tmplContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	// Build template data from request
	data := buildTemplateData(req, config)

	// Render the template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// loadTemplate loads a template from the filesystem or embedded resources
// Priority order:
// 1. CustomTemplatePath (if specified in config)
// 2. .github/gh-review-responder/prompts/<name>
// 3. ~/.config/gh-review-responder/prompts/<name>
// 4. Embedded default template
func loadTemplate(name string, config *TemplateConfig) (string, error) {
	// 1. Check custom template path from config
	if config.CustomTemplatePath != "" {
		content, err := os.ReadFile(config.CustomTemplatePath)
		if err == nil {
			return string(content), nil
		}
		// If explicitly specified but not found, return error
		return "", fmt.Errorf("custom template not found: %s", config.CustomTemplatePath)
	}

	// 2. Check repo-specific template
	repoPath := filepath.Join(".github", "gh-review-responder", "prompts", name)
	if content, err := os.ReadFile(repoPath); err == nil {
		return string(content), nil
	}

	// 3. Check user-level template
	homeDir, err := os.UserHomeDir()
	if err == nil {
		userPath := filepath.Join(homeDir, ".config", "gh-review-responder", "prompts", name)
		if content, err := os.ReadFile(userPath); err == nil {
			return string(content), nil
		}
	}

	// 4. Use embedded default template
	embeddedPath := filepath.Join("templates", name)
	content, err := embeddedTemplates.ReadFile(embeddedPath)
	if err != nil {
		return "", fmt.Errorf("failed to load embedded template: %w", err)
	}

	return string(content), nil
}

// buildTemplateData creates the data map for template rendering
func buildTemplateData(req *SuggestionRequest, config *TemplateConfig) map[string]any {
	data := map[string]any{
		"ReviewComment":      req.ReviewComment,
		"SuggestedCode":      req.SuggestedCode,
		"OriginalDiffHunk":   req.OriginalDiffHunk,
		"FilePath":           req.FilePath,
		"FileLanguage":       req.FileLanguage,
		"CurrentFileContent": req.CurrentFileContent,
		"TargetLineNumber":   req.TargetLineNumber + 1, // Convert to 1-based for display
		"ExpectedLines":      req.ExpectedLines,
		"MismatchDetails":    req.MismatchDetails,
		"CommentID":          req.CommentID,
	}

	// Merge custom variables (allowing overrides)
	if config.CustomVariables != nil {
		maps.Copy(data, config.CustomVariables)
	}

	return data
}
