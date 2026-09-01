package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gh-tui-tools/gh-review-responder/pkg/ai"
	"github.com/gh-tui-tools/gh-review-responder/pkg/applier"
	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
	"github.com/spf13/cobra"
)

var (
	applyAll          bool
	applyFile         string
	applyShowResolved bool
	applyDebug        bool
	applyAIAuto       bool
	applyAIProvider   string
	applyAIModel      string
	applyAITemplate   string
	applyAIToken      string
)

var applyCmd = &cobra.Command{
	Use:   "apply [PR_NUMBER]",
	Short: "Apply review suggestions to local files",
	Long:  `Apply GitHub review suggestions to your local files interactively or in batch mode.`,
	RunE:  runApply,
}

func init() {
	applyCmd.Flags().BoolVar(&applyAll, "all", false, "Apply all suggestions without prompting")
	applyCmd.Flags().StringVar(&applyFile, "file", "", "Only apply suggestions for a specific file")
	applyCmd.Flags().BoolVar(&applyShowResolved, "include-resolved", false, "Include resolved/done suggestions")
	applyCmd.Flags().BoolVar(&applyDebug, "debug", false, "Enable debug output")

	// AI flags
	applyCmd.Flags().BoolVar(&applyAIAuto, "ai-auto", false, "Automatically apply all suggestions using AI")
	applyCmd.Flags().StringVar(&applyAIProvider, "ai-provider", "", "AI provider to use (gemini) - defaults to env or 'gemini'")
	applyCmd.Flags().StringVar(&applyAIModel, "ai-model", "", "AI model to use (provider-specific)")
	applyCmd.Flags().StringVar(&applyAITemplate, "ai-template", "", "Custom AI prompt template file")
	applyCmd.Flags().StringVar(&applyAIToken, "ai-token", "", "AI API token/key (alternative to environment variable)")
}

func runApply(cmd *cobra.Command, args []string) error {
	// Check if there are uncommitted changes
	if err := checkCleanWorkingDirectory(); err != nil {
		return err
	}

	client := github.NewClient()
	client.SetDebug(applyDebug)
	if repoFlag != "" {
		client.SetRepo(repoFlag)
	}

	prNumber, err := getPRNumberWithSelection(args, client)
	if err != nil {
		return err
	}

	comments, err := client.FetchReviewComments(prNumber)
	if err != nil {
		return fmt.Errorf("failed to fetch review comments: %w", err)
	}

	// Filter comments with suggestions and not resolved (unless --include-resolved)
	suggestions := make([]*github.ReviewComment, 0)
	for _, comment := range comments {
		if comment.HasSuggestion {
			// Skip resolved suggestions unless explicitly requested
			if !applyShowResolved && comment.IsResolved() {
				continue
			}
			if applyFile == "" || comment.Path == applyFile {
				suggestions = append(suggestions, comment)
			}
		}
	}

	if len(suggestions) == 0 {
		if applyFile != "" {
			fmt.Printf("No unresolved suggestions found for file: %s\n", applyFile)
		} else {
			fmt.Println("No unresolved suggestions found in review comments.")
		}
		if !applyShowResolved {
			fmt.Println("Use --include-resolved to show resolved suggestions.")
		}
		return nil
	}

	fmt.Printf("Found %d suggestion(s) to apply\n\n", len(suggestions))

	app := applier.New()
	app.SetDebug(applyDebug)
	app.SetGitHubClient(client) // Pass GitHub client for resolving threads

	// Setup AI provider if needed (for interactive or --ai-auto)
	if applyAIAuto || (!applyAll) {
		provider, err := setupAIProvider()
		if err != nil {
			if applyAIAuto {
				// AI is required for --ai-auto
				return fmt.Errorf("AI provider required for --ai-auto: %w", err)
			}
			// In interactive mode, just warn that AI won't be available
			if applyDebug {
				fmt.Fprintf(os.Stderr, "Note: AI features not available: %v\n", err)
			}
		} else {
			app.SetAIProvider(provider)
			if applyDebug {
				fmt.Fprintf(os.Stderr, "AI provider configured: %s\n", provider.Name())
			}
		}
	}

	if applyAIAuto {
		return app.ApplyAllWithAI(suggestions)
	}

	if applyAll {
		return app.ApplyAll(suggestions)
	}

	return app.ApplyInteractive(suggestions)
}

// checkCleanWorkingDirectory checks if the git working directory is clean
func checkCleanWorkingDirectory() error {
	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		// If git status fails, we're probably not in a git repo
		return fmt.Errorf("failed to check git status: %w", err)
	}

	// If there's any output, there are uncommitted changes
	if len(output) > 0 {
		return fmt.Errorf("working directory has uncommitted changes. Please stash or commit them first:\n  git stash")
	}

	return nil
}

// setupAIProvider creates and configures an AI provider based on flags and environment
func setupAIProvider() (ai.AIProvider, error) {
	// Start with config from environment
	config := ai.LoadConfigFromEnv()

	// Override with command-line flags if provided
	if applyAIProvider != "" {
		config.Provider = applyAIProvider
	}
	if applyAIModel != "" {
		config.Model = applyAIModel
	}
	if applyAITemplate != "" {
		config.CustomTemplatePath = applyAITemplate
	}
	if applyAIToken != "" {
		config.APIKey = applyAIToken
	}

	// Validate we have an API key
	if config.APIKey == "" {
		meta, ok := ai.GetProviderMetadata(config.Provider)
		if !ok || len(meta.EnvVars) == 0 {
			return nil, fmt.Errorf("AI API key not found for provider %q. Use --ai-token flag or set the appropriate environment variable", config.Provider)
		}

		providerLabel := meta.Label
		if providerLabel == "" {
			providerLabel = strings.ToUpper(config.Provider)
		}

		return nil, fmt.Errorf("%s API key not found. Set %s or use --ai-token flag",
			providerLabel, strings.Join(meta.EnvVars, " or "))
	}

	// Create the provider
	return ai.NewProviderFromConfig(config)
}
