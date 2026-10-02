package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"os"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Generate cli documentation",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := os.MkdirAll("./docs", 0755); err != nil {
			return err
		}
		return doc.GenMarkdownTree(rootCmd, "./docs")
	},
}

func init() {
	rootCmd.AddCommand(docsCmd)
}
