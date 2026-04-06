package cmd

import (
	"fmt"
	"os"

	"github.com/versionlens/OpenNalvin/internal/epubextract"
	fileservepkg "github.com/versionlens/OpenNalvin/internal/fileserve"
	"github.com/versionlens/OpenNalvin/internal/output"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	filesURLBase          string
	filesExtractEPUBOut   string
	filesExtractEPUBForce bool
)

type fileURLPayload struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	BaseURL   string `json:"base_url"`
	URL       string `json:"url"`
}

var filesCmd = &cobra.Command{
	Use:   "files",
	Short: "Serve-aware workspace file helpers",
}

var filesURLCmd = &cobra.Command{
	Use:   "url <workspace-relative-path>",
	Short: "Build a full URL for a workspace file served by nalvin serve",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		relativePath, absolutePath, err := workspacepkg.ResolveFilePathForPaths(paths, args[0], false)
		if err != nil {
			return err
		}

		info, err := os.Stat(absolutePath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("path refers to a directory; use a file path instead")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("path must refer to a regular file")
		}

		fullURL, baseURL, err := fileservepkg.BuildWorkspaceFileURL(cfg, paths.Name, relativePath, filesURLBase)
		if err != nil {
			return err
		}

		payload := fileURLPayload{
			Workspace: paths.Name,
			Path:      relativePath,
			BaseURL:   baseURL,
			URL:       fullURL,
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("%s", payload.URL)
		return nil
	},
}

var filesExtractEPUBCmd = &cobra.Command{
	Use:   "extract-epub <workspace-relative-epub>",
	Short: "Extract an EPUB into Markdown chapters under the active workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		result, err := epubextract.Extract(paths, epubextract.Options{
			InputPath:  args[0],
			OutputPath: filesExtractEPUBOut,
			Force:      filesExtractEPUBForce,
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}
		w.Line("Book Title: %s", result.BookTitle)
		if result.Author != "" {
			w.Line("Author: %s", result.Author)
		}
		w.Line("Output Directory: %s", result.OutputPath)
		w.Line("Chapter Count: %d", result.ChapterCount)
		return nil
	},
}

func init() {
	filesURLCmd.Flags().StringVar(&filesURLBase, "base-url", "", "optional absolute base URL override, for example http://127.0.0.1:4210")
	filesExtractEPUBCmd.Flags().StringVar(&filesExtractEPUBOut, "out", "", "optional workspace-relative output directory")
	filesExtractEPUBCmd.Flags().BoolVar(&filesExtractEPUBForce, "force", false, "replace an existing output directory")
	filesCmd.AddCommand(filesURLCmd)
	filesCmd.AddCommand(filesExtractEPUBCmd)
	rootCmd.AddCommand(filesCmd)
}
