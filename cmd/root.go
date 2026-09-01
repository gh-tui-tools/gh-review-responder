package cmd

import (
	"os"

	"github.com/gh-tui-tools/gh-review-responder/pkg/ui"
	"github.com/spf13/cobra"
)

var (
	repoFlag string
	noColor  bool
)

var rootCmd = &cobra.Command{
	Use:   "gh-review-responder",
	Short: "Apply GitHub review comments directly to your code",
	Long: `gh-review-responder is a GitHub CLI extension that allows you to fetch and apply
review comments and suggestions from pull requests directly to your local code.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		ui.SetColorEnabled(!noColor)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return cmd.Help()
		}
		return browseCmd.RunE(browseCmd, []string{})
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		noColor = true
	}

	rootCmd.PersistentFlags().StringVarP(&repoFlag, "repo", "R", "", "Select a repository using the OWNER/REPO format")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(resolveCmd)
	rootCmd.AddCommand(commentCmd)
	rootCmd.AddCommand(browseCmd)
}
