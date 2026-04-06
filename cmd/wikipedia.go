package cmd

import (
	"context"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/output"
	wikipediapkg "github.com/versionlens/OpenNalvin/internal/wikipedia"
	"github.com/spf13/cobra"
)

var (
	wikipediaLanguage     string
	wikipediaSectionIndex string
)

var wikipediaFetch = func(ctx context.Context, req wikipediapkg.FetchRequest) (wikipediapkg.FetchResult, error) {
	return wikipediapkg.NewClient(wikipediapkg.Config{}).Fetch(ctx, req)
}

var wikipediaCmd = &cobra.Command{
	Use:   "wikipedia",
	Short: "Fetch Wikipedia summaries and section data without auth",
}

var wikipediaGetCmd = &cobra.Command{
	Use:   "get <title>",
	Short: "Fetch a Wikipedia page summary, section index, and optional section content",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := wikipediaFetch(cmd.Context(), wikipediapkg.FetchRequest{
			Title:        args[0],
			Language:     wikipediaLanguage,
			SectionIndex: wikipediaSectionIndex,
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}

		w.Line("Title: %s", result.Title)
		if result.Summary.Description != "" {
			w.Line("Description: %s", result.Summary.Description)
		}
		if result.ArticleURL != "" {
			w.Line("Article URL: %s", result.ArticleURL)
		}
		if result.Summary.Extract != "" {
			w.Line("")
			w.Line("%s", result.Summary.Extract)
		}
		if result.Section == nil {
			if len(result.Sections) > 0 {
				w.Line("")
				w.Line("Sections:")
				for _, section := range result.Sections {
					number := strings.TrimSpace(section.Number)
					if number != "" {
						w.Line("  %s (%s) %s", section.Index, number, section.Title)
						continue
					}
					w.Line("  %s %s", section.Index, section.Title)
				}
			}
			return nil
		}

		w.Line("")
		if result.Section.Number != "" {
			w.Line("Section %s (%s): %s", result.Section.Index, result.Section.Number, result.Section.Title)
		} else {
			w.Line("Section %s: %s", result.Section.Index, result.Section.Title)
		}
		if result.Section.Text != "" {
			w.Line("")
			w.Line("%s", result.Section.Text)
		}
		return nil
	},
}

func init() {
	wikipediaGetCmd.Flags().StringVar(&wikipediaLanguage, "lang", "", "optional Wikipedia language code, for example en or sv")
	wikipediaGetCmd.Flags().StringVar(&wikipediaSectionIndex, "section-index", "", "optional section index to fetch, for example 1 or 2")
	wikipediaCmd.AddCommand(wikipediaGetCmd)
	rootCmd.AddCommand(wikipediaCmd)
}
