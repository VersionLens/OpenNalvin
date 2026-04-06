package epubextract

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/raitucarp/epub"
	"golang.org/x/net/html"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

type Options struct {
	InputPath  string
	OutputPath string
	Force      bool
}

type Result struct {
	InputPath    string    `json:"input_path"`
	OutputPath   string    `json:"output_path"`
	BookTitle    string    `json:"book_title"`
	Author       string    `json:"author,omitempty"`
	ChapterCount int       `json:"chapter_count"`
	TOCPath      string    `json:"toc_path"`
	Chapters     []Chapter `json:"chapters"`
}

type Chapter struct {
	Index      int    `json:"index"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	SourceHref string `json:"source_href,omitempty"`
}

type extractor struct {
	paths          workspace.Paths
	outputRel      string
	outputAbs      string
	book           epub.Reader
	markdownByID   map[string]string
	htmlByID       map[string]*html.Node
	resourceByHref map[string]epub.PublicationResource
	chapters       []Chapter
	emittedByHref  map[string]Chapter
	tocLines       []tocLine
	nextIndex      int
	usedSlugs      map[string]int
}

type tocLine struct {
	Depth int
	Title string
	Link  string
}

var (
	nonSlugCharPattern = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	frontMatterPattern = regexp.MustCompile(`(?s)\A---\n.*?\n---\n*`)
)

func Extract(paths workspace.Paths, opts Options) (Result, error) {
	inputRel, inputAbs, err := workspace.ResolveFilePathForPaths(paths, opts.InputPath, false)
	if err != nil {
		return Result{}, err
	}

	info, err := os.Stat(inputAbs)
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("input path must refer to a regular file")
	}

	outputPath := strings.TrimSpace(opts.OutputPath)
	if outputPath == "" {
		base := sanitizeSlug(filepath.Base(strings.TrimSuffix(inputRel, filepath.Ext(inputRel))))
		if base == "" {
			base = "book"
		}
		outputPath = base + "_extracted"
	}

	outputRel, outputAbs, err := workspace.ResolveFilePathForPaths(paths, outputPath, false)
	if err != nil {
		return Result{}, err
	}

	if err := prepareOutputDir(inputAbs, outputAbs, opts.Force); err != nil {
		return Result{}, err
	}

	book, err := epub.OpenReader(inputAbs)
	if err != nil {
		return Result{}, fmt.Errorf("open epub: %w", err)
	}

	ex := extractor{
		paths:          paths,
		outputRel:      outputRel,
		outputAbs:      outputAbs,
		book:           book,
		markdownByID:   book.ContentDocumentMarkdown(),
		htmlByID:       book.ContentDocumentXHTML(),
		resourceByHref: make(map[string]epub.PublicationResource),
		emittedByHref:  make(map[string]Chapter),
		usedSlugs:      make(map[string]int),
		nextIndex:      1,
	}
	for _, resource := range book.Resources() {
		ex.resourceByHref[normalizeHref(resource.Href)] = resource
	}

	extractedFromTOC, err := ex.extractFromTOC()
	if err != nil {
		return Result{}, err
	}
	if !extractedFromTOC {
		if err := ex.extractFromSpine(); err != nil {
			return Result{}, err
		}
	}

	if len(ex.chapters) == 0 {
		return Result{}, fmt.Errorf("no extractable EPUB chapters found")
	}

	tocRel := filepath.ToSlash(filepath.Join(outputRel, "TOC.md"))
	tocAbs := filepath.Join(outputAbs, "TOC.md")
	if err := os.WriteFile(tocAbs, []byte(ex.renderTOC()), 0o644); err != nil {
		return Result{}, fmt.Errorf("write TOC: %w", err)
	}

	bookTitle := strings.TrimSpace(book.Title())
	if bookTitle == "" {
		bookTitle = strings.TrimSpace(filepath.Base(strings.TrimSuffix(inputRel, filepath.Ext(inputRel))))
	}

	return Result{
		InputPath:    inputRel,
		OutputPath:   outputRel,
		BookTitle:    bookTitle,
		Author:       strings.TrimSpace(book.Author()),
		ChapterCount: len(ex.chapters),
		TOCPath:      tocRel,
		Chapters:     append([]Chapter(nil), ex.chapters...),
	}, nil
}

func prepareOutputDir(inputAbs, outputAbs string, force bool) error {
	info, err := os.Stat(outputAbs)
	switch {
	case err == nil:
		if !force {
			return fmt.Errorf("output path already exists: %s", filepath.Base(outputAbs))
		}
		if pathContains(outputAbs, inputAbs) {
			return fmt.Errorf("cannot replace output directory that contains the input EPUB")
		}
		if err := os.RemoveAll(outputAbs); err != nil {
			return fmt.Errorf("remove existing output path: %w", err)
		}
		if info != nil && !info.IsDir() {
			// os.RemoveAll already removed the file; continue by recreating the directory.
		}
	case os.IsNotExist(err):
		// Continue below.
	default:
		return err
	}

	if err := os.MkdirAll(outputAbs, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	return nil
}

func (ex *extractor) extractFromTOC() (bool, error) {
	toc, err := ex.book.TableOfContents()
	if err != nil {
		return false, nil
	}

	items := toc.Items
	if toc.Href != "" || len(items) == 0 {
		items = []epub.TOC{toc}
	}

	before := len(ex.chapters)
	for _, item := range items {
		if err := ex.walkTOCItem(item, 0); err != nil {
			return false, err
		}
	}
	return len(ex.chapters) > before, nil
}

func (ex *extractor) walkTOCItem(item epub.TOC, depth int) error {
	title := strings.TrimSpace(item.Title)
	link := ""

	if normalizedHref := normalizeHref(item.Href); normalizedHref != "" {
		chapter, ok, err := ex.ensureChapter(normalizedHref, item.Href, title)
		if err != nil {
			return err
		}
		if ok {
			if title == "" {
				title = chapter.Title
			}
			link = path.Base(chapter.Path)
		}
	}

	if title != "" || link != "" {
		ex.tocLines = append(ex.tocLines, tocLine{
			Depth: depth,
			Title: title,
			Link:  link,
		})
	}

	for _, child := range item.Items {
		if err := ex.walkTOCItem(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (ex *extractor) extractFromSpine() error {
	for _, resource := range ex.book.Spine() {
		normalizedHref := normalizeHref(resource.Href)
		if normalizedHref == "" || resource.Properties == "nav" {
			continue
		}
		if _, ok := ex.markdownByID[resource.ID]; !ok {
			continue
		}

		chapter, _, err := ex.ensureChapter(normalizedHref, resource.Href, "")
		if err != nil {
			return err
		}
		ex.tocLines = append(ex.tocLines, tocLine{
			Depth: 0,
			Title: chapter.Title,
			Link:  path.Base(chapter.Path),
		})
	}
	return nil
}

func (ex *extractor) ensureChapter(normalizedHref, sourceHref, titleHint string) (Chapter, bool, error) {
	if chapter, ok := ex.emittedByHref[normalizedHref]; ok {
		return chapter, true, nil
	}

	resource, ok := ex.resourceByHref[normalizedHref]
	if !ok {
		return Chapter{}, false, nil
	}

	markdown := strings.TrimSpace(stripFrontMatter(ex.markdownByID[resource.ID]))
	title := strings.TrimSpace(titleHint)
	if title == "" {
		title = deriveTitle(ex.htmlByID[resource.ID])
	}
	if title == "" {
		title = resourceTitleFallback(resource.Href)
	}

	filename := fmt.Sprintf("%03d_%s.md", ex.nextIndex, ex.uniqueSlug(title, resource.Href))
	chapterRel := filepath.ToSlash(filepath.Join(ex.outputRel, filename))
	chapterAbs := filepath.Join(ex.outputAbs, filename)

	content := "# " + title + "\n"
	if markdown != "" {
		content += "\n" + markdown + "\n"
	}

	if err := os.WriteFile(chapterAbs, []byte(content), 0o644); err != nil {
		return Chapter{}, false, fmt.Errorf("write chapter %q: %w", title, err)
	}

	chapter := Chapter{
		Index:      ex.nextIndex,
		Title:      title,
		Path:       chapterRel,
		SourceHref: strings.TrimSpace(sourceHref),
	}
	ex.nextIndex++
	ex.chapters = append(ex.chapters, chapter)
	ex.emittedByHref[normalizedHref] = chapter
	return chapter, true, nil
}

func (ex *extractor) renderTOC() string {
	var b strings.Builder
	b.WriteString("# Table of Contents\n\n")
	for _, line := range ex.tocLines {
		if line.Title == "" {
			continue
		}
		b.WriteString(strings.Repeat("  ", line.Depth))
		if line.Link != "" {
			b.WriteString(fmt.Sprintf("- [%s](%s)\n", escapeInlineMarkdown(line.Title), line.Link))
			continue
		}
		b.WriteString("- " + escapeInlineMarkdown(line.Title) + "\n")
	}
	return b.String()
}

func pathContains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func normalizeHref(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if index := strings.Index(href, "#"); index >= 0 {
		href = href[:index]
	}
	if href == "" || href == "." {
		return ""
	}
	return path.Clean(href)
}

func resourceTitleFallback(href string) string {
	base := strings.TrimSpace(path.Base(normalizeHref(href)))
	base = strings.TrimSuffix(base, path.Ext(base))
	if base == "" {
		return "Untitled Chapter"
	}
	return base
}

func sanitizeSlug(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	transformer := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	value, _, _ = transform.String(transformer, value)
	value = nonSlugCharPattern.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	value = strings.ToLower(value)
	return value
}

func (ex *extractor) uniqueSlug(title, href string) string {
	slug := sanitizeSlug(title)
	if slug == "" {
		slug = sanitizeSlug(resourceTitleFallback(href))
	}
	if slug == "" {
		slug = "chapter"
	}
	if count := ex.usedSlugs[slug]; count > 0 {
		ex.usedSlugs[slug] = count + 1
		return fmt.Sprintf("%s_%d", slug, count+1)
	}
	ex.usedSlugs[slug] = 1
	return slug
}

func stripFrontMatter(markdown string) string {
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	return strings.TrimSpace(frontMatterPattern.ReplaceAllString(markdown, ""))
}

func deriveTitle(root *html.Node) string {
	for _, tag := range []string{"h1", "h2", "h3", "h4", "h5", "h6", "title"} {
		if title := firstNodeText(root, tag); title != "" {
			return title
		}
	}
	return ""
}

func firstNodeText(node *html.Node, tag string) string {
	if node == nil {
		return ""
	}
	if node.Type == html.ElementNode && strings.EqualFold(node.Data, tag) {
		return strings.TrimSpace(nodeText(node))
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if text := firstNodeText(child, tag); text != "" {
			return text
		}
	}
	return ""
}

func nodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == html.TextNode {
		return node.Data
	}
	var b strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text := strings.TrimSpace(nodeText(child))
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return b.String()
}

func escapeInlineMarkdown(text string) string {
	text = strings.ReplaceAll(text, "[", `\[`)
	text = strings.ReplaceAll(text, "]", `\]`)
	return text
}
