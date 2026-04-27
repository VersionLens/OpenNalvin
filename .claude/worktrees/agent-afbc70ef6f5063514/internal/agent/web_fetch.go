package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	readability "github.com/go-shiori/go-readability"
)

const (
	webFetchJSONBodyLimit  = 32 * 1024
	webFetchBodyTokenLimit = 5000
)

type webFetchResponsePayload struct {
	URL             string      `json:"url"`
	Body            string      `json:"body"`
	BodyJSON        any         `json:"body_json,omitempty"`
	Truncated       bool        `json:"truncated"`
	SavedToFilepath string      `json:"saved_to_filepath,omitempty"`
	Mode            string      `json:"mode,omitempty"`
	Title           string      `json:"title,omitempty"`
	Excerpt         string      `json:"excerpt,omitempty"`
	Encoding        string      `json:"encoding,omitempty"`
	Selector        string      `json:"selector,omitempty"`
}

type readableArticle struct {
	Title   string
	Excerpt string
	Body    string
}

// validateWebFetchURL rejects URLs with unsupported schemes. The primary goal
// is to catch the common mistake of passing a local file path or workspace-
// relative path to web_fetch_get instead of using the view/ls/glob tools.
func validateWebFetchURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https":
		return nil
	case "":
		return fmt.Errorf("url must include an http:// or https:// scheme (got %q); example: https://example.com/path", rawURL)
	case "file":
		return fmt.Errorf("url scheme %q is not supported; only http and https are allowed; use the view, ls, or glob tools to read local files", scheme)
	default:
		return fmt.Errorf("url scheme %q is not supported; only http and https are allowed", scheme)
	}
}

func webFetchShouldExtractArticleContent(input webFetchInput) bool {
	if input.ExtractArticleContent == nil {
		return true
	}
	return *input.ExtractArticleContent
}

func truncateStringToTokenLimit(value string, maxTokens int, model string) (string, bool) {
	if value == "" || maxTokens <= 0 {
		return "", value != ""
	}

	counter := newTokenEstimator(model)
	if counter == nil || counter.Count(value) <= int64(maxTokens) {
		return value, false
	}

	boundaries := make([]int, 0, utf8.RuneCountInString(value)+1)
	for index := range value {
		boundaries = append(boundaries, index)
	}
	boundaries = append(boundaries, len(value))

	low := 0
	high := len(boundaries) - 1
	best := 0
	for low <= high {
		mid := low + (high-low)/2
		candidate := value[:boundaries[mid]]
		if counter.Count(candidate) <= int64(maxTokens) {
			best = boundaries[mid]
			low = mid + 1
			continue
		}
		high = mid - 1
	}

	return value[:best], true
}

func truncateWebFetchBodyForTokens(rt *agentRuntime, value string) (string, bool) {
	model := ""
	if rt != nil {
		model = rt.model
	}
	return truncateStringToTokenLimit(value, webFetchBodyTokenLimit, model)
}

func webFetchPreviewBytes(body []byte) ([]byte, bool) {
	if len(body) <= webFetchJSONBodyLimit {
		return body, false
	}
	return body[:webFetchJSONBodyLimit], true
}

func normalizeFetchBodyString(body []byte) string {
	return string(bytes.ToValidUTF8(body, []byte("\uFFFD")))
}

func firstBytes(body []byte, limit int) []byte {
	if limit <= 0 || len(body) <= limit {
		return body
	}
	return body[:limit]
}

func isJSONLikeContentType(contentType string) bool {
	lowerContentType := strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(lowerContentType, "json")
}

func isHTMLLikeResponse(contentType string, body []byte) bool {
	lowerContentType := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(lowerContentType, "html") {
		return true
	}

	sample := strings.ToLower(strings.TrimSpace(normalizeFetchBodyString(firstBytes(body, 2048))))
	if sample == "" {
		return false
	}

	for _, marker := range []string{"<!doctype html", "<html", "<head", "<body", "<article"} {
		if strings.HasPrefix(sample, marker) || strings.Contains(sample, marker) {
			return true
		}
	}
	return false
}

func isTextLikeResponse(contentType string, body []byte) bool {
	if isJSONLikeContentType(contentType) || isHTMLLikeResponse(contentType, body) {
		return true
	}

	lowerContentType := strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(lowerContentType, "text/") {
		return true
	}
	for _, marker := range []string{
		"xml",
		"+xml",
		"yaml",
		"csv",
		"javascript",
		"ecmascript",
		"svg",
		"x-www-form-urlencoded",
	} {
		if strings.Contains(lowerContentType, marker) {
			return true
		}
	}

	sample := firstBytes(body, 2048)
	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	if !utf8.Valid(sample) {
		return false
	}

	sniffed := strings.ToLower(http.DetectContentType(sample))
	return strings.HasPrefix(sniffed, "text/") || strings.Contains(sniffed, "json") || strings.Contains(sniffed, "xml")
}

func buildReaderModeBody(pageURL string, body []byte) (readableArticle, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil {
		return readableArticle{}, fmt.Errorf("parse page url: %w", err)
	}

	article, err := readability.FromReader(bytes.NewReader(body), parsedURL)
	if err != nil {
		return readableArticle{}, err
	}

	textBody := strings.TrimSpace(article.TextContent)
	if textBody == "" {
		return readableArticle{}, fmt.Errorf("no readable article content extracted")
	}

	return readableArticle{
		Title:   strings.TrimSpace(article.Title),
		Excerpt: strings.TrimSpace(article.Excerpt),
		Body:    textBody,
	}, nil
}

func selectHTMLNodes(body []byte, selector string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	selection := doc.Find(selector)
	if selection.Length() == 0 {
		return "", nil
	}

	matches := make([]string, 0, selection.Length())
	var firstErr error
	selection.Each(func(_ int, item *goquery.Selection) {
		if firstErr != nil {
			return
		}
		rendered, err := goquery.OuterHtml(item)
		if err != nil {
			firstErr = err
			return
		}
		matches = append(matches, rendered)
	})
	if firstErr != nil {
		return "", firstErr
	}

	return strings.Join(matches, "\n\n"), nil
}

func encodeBinaryFetchBody(body []byte) string {
	return base64.StdEncoding.EncodeToString(body)
}
