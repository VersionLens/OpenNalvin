package wikipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	defaultLanguage  = "en"
	defaultUserAgent = "nalvin/1.0"
)

type Config struct {
	HTTPClient             *http.Client
	UserAgent              string
	SummaryBaseURLTemplate string
	ActionBaseURLTemplate  string
}

type Client struct {
	httpClient             *http.Client
	userAgent              string
	summaryBaseURLTemplate string
	actionBaseURLTemplate  string
}

type FetchRequest struct {
	Title        string
	Language     string
	SectionIndex string
}

type FetchResult struct {
	Language   string         `json:"language"`
	Title      string         `json:"title"`
	PageID     int            `json:"page_id"`
	ArticleURL string         `json:"article_url,omitempty"`
	Summary    Summary        `json:"summary"`
	Sections   []SectionIndex `json:"sections"`
	Section    *Section       `json:"section,omitempty"`
}

type Summary struct {
	Type        string `json:"type,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Extract     string `json:"extract,omitempty"`
	ExtractHTML string `json:"extract_html,omitempty"`
	Revision    string `json:"revision,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
}

type SectionIndex struct {
	Index    string `json:"index"`
	Number   string `json:"number,omitempty"`
	Title    string `json:"title"`
	Anchor   string `json:"anchor,omitempty"`
	TOCLevel int    `json:"toc_level,omitempty"`
	HLevel   int    `json:"h_level,omitempty"`
}

type Section struct {
	Index    string `json:"index"`
	Number   string `json:"number,omitempty"`
	Title    string `json:"title"`
	Anchor   string `json:"anchor,omitempty"`
	TOCLevel int    `json:"toc_level,omitempty"`
	HLevel   int    `json:"h_level,omitempty"`
	HTML     string `json:"html"`
	Text     string `json:"text"`
}

type summaryResponse struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	PageID      int    `json:"pageid"`
	Revision    string `json:"revision"`
	Timestamp   string `json:"timestamp"`
	Description string `json:"description"`
	Extract     string `json:"extract"`
	ExtractHTML string `json:"extract_html"`
	ContentURLs struct {
		Desktop struct {
			Page string `json:"page"`
		} `json:"desktop"`
	} `json:"content_urls"`
}

type parseTOCResponse struct {
	Parse struct {
		Title   string `json:"title"`
		PageID  int    `json:"pageid"`
		TOCData struct {
			Sections []struct {
				TOCLevel int    `json:"tocLevel"`
				HLevel   int    `json:"hLevel"`
				Line     string `json:"line"`
				Number   string `json:"number"`
				Index    string `json:"index"`
				Anchor   string `json:"anchor"`
			} `json:"sections"`
		} `json:"tocdata"`
	} `json:"parse"`
}

type parseSectionResponse struct {
	Parse struct {
		Title  string `json:"title"`
		PageID int    `json:"pageid"`
		Text   string `json:"text"`
	} `json:"parse"`
}

type apiErrorResponse struct {
	Error *struct {
		Code string `json:"code"`
		Info string `json:"info"`
	} `json:"error"`
}

func NewClient(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	summaryBase := strings.TrimSpace(cfg.SummaryBaseURLTemplate)
	if summaryBase == "" {
		summaryBase = "https://%s.wikipedia.org"
	}
	actionBase := strings.TrimSpace(cfg.ActionBaseURLTemplate)
	if actionBase == "" {
		actionBase = "https://%s.wikipedia.org"
	}

	return &Client{
		httpClient:             httpClient,
		userAgent:              userAgent,
		summaryBaseURLTemplate: summaryBase,
		actionBaseURLTemplate:  actionBase,
	}
}

func (c *Client) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return FetchResult{}, fmt.Errorf("title is required")
	}

	lang := normalizeLanguage(req.Language)
	summary, err := c.fetchSummary(ctx, lang, title)
	if err != nil {
		return FetchResult{}, err
	}
	sections, pageID, pageTitle, err := c.fetchSectionIndex(ctx, lang, title)
	if err != nil {
		return FetchResult{}, err
	}

	result := FetchResult{
		Language:   lang,
		Title:      coalesce(pageTitle, summary.Title, title),
		PageID:     firstNonZero(pageID, summary.PageID),
		ArticleURL: strings.TrimSpace(summary.ContentURLs.Desktop.Page),
		Summary: Summary{
			Type:        strings.TrimSpace(summary.Type),
			Title:       coalesce(summary.Title, title),
			Description: strings.TrimSpace(summary.Description),
			Extract:     strings.TrimSpace(summary.Extract),
			ExtractHTML: strings.TrimSpace(summary.ExtractHTML),
			Revision:    strings.TrimSpace(summary.Revision),
			Timestamp:   strings.TrimSpace(summary.Timestamp),
		},
		Sections: sections,
	}

	if strings.TrimSpace(req.SectionIndex) != "" {
		selected, err := c.fetchSection(ctx, lang, title, strings.TrimSpace(req.SectionIndex))
		if err != nil {
			return FetchResult{}, err
		}
		if meta, ok := findSectionMeta(sections, req.SectionIndex); ok {
			selected.Index = meta.Index
			selected.Number = meta.Number
			selected.Title = coalesce(meta.Title, selected.Title)
			selected.Anchor = coalesce(meta.Anchor, selected.Anchor)
			selected.TOCLevel = firstNonZero(meta.TOCLevel, selected.TOCLevel)
			selected.HLevel = firstNonZero(meta.HLevel, selected.HLevel)
		}
		result.Section = selected
	}

	return result, nil
}

func (c *Client) fetchSummary(ctx context.Context, language, title string) (summaryResponse, error) {
	endpoint := resolveBaseURL(c.summaryBaseURLTemplate, language) + "/api/rest_v1/page/summary/" + url.PathEscape(title)
	var payload summaryResponse
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		return summaryResponse{}, fmt.Errorf("fetch wikipedia summary: %w", err)
	}
	return payload, nil
}

func (c *Client) fetchSectionIndex(ctx context.Context, language, title string) ([]SectionIndex, int, string, error) {
	values := url.Values{}
	values.Set("action", "parse")
	values.Set("page", title)
	values.Set("prop", "tocdata")
	values.Set("format", "json")
	values.Set("formatversion", "2")

	endpoint := resolveBaseURL(c.actionBaseURLTemplate, language) + "/w/api.php?" + values.Encode()
	var payload parseTOCResponse
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		return nil, 0, "", fmt.Errorf("fetch wikipedia section index: %w", err)
	}

	items := make([]SectionIndex, 0, len(payload.Parse.TOCData.Sections))
	for _, item := range payload.Parse.TOCData.Sections {
		items = append(items, SectionIndex{
			Index:    strings.TrimSpace(item.Index),
			Number:   strings.TrimSpace(item.Number),
			Title:    strings.TrimSpace(item.Line),
			Anchor:   strings.TrimSpace(item.Anchor),
			TOCLevel: item.TOCLevel,
			HLevel:   item.HLevel,
		})
	}
	return items, payload.Parse.PageID, strings.TrimSpace(payload.Parse.Title), nil
}

func (c *Client) fetchSection(ctx context.Context, language, title, sectionIndex string) (*Section, error) {
	values := url.Values{}
	values.Set("action", "parse")
	values.Set("page", title)
	values.Set("prop", "text")
	values.Set("section", sectionIndex)
	values.Set("format", "json")
	values.Set("formatversion", "2")

	endpoint := resolveBaseURL(c.actionBaseURLTemplate, language) + "/w/api.php?" + values.Encode()
	var payload parseSectionResponse
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		return nil, fmt.Errorf("fetch wikipedia section %s: %w", sectionIndex, err)
	}

	html := strings.TrimSpace(payload.Parse.Text)
	return &Section{
		Index: sectionIndex,
		Title: firstHeadingText(html),
		HTML:  html,
		Text:  extractSectionText(html),
	}, nil
}

func (c *Client) getJSON(ctx context.Context, rawURL string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err == nil && apiErr.Error != nil {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(apiErr.Error.Info))
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func resolveBaseURL(template, language string) string {
	if strings.Contains(template, "%s") {
		return strings.TrimRight(fmt.Sprintf(template, language), "/")
	}
	return strings.TrimRight(template, "/")
}

func normalizeLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return defaultLanguage
	}
	return language
}

func extractSectionText(rawHTML string) string {
	if strings.TrimSpace(rawHTML) == "" {
		return ""
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return strings.TrimSpace(rawHTML)
	}

	for _, selector := range []string{
		".mw-editsection",
		".reference",
		".reflist",
		"style",
		"script",
		"sup",
		"table",
	} {
		doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
			s.Remove()
		})
	}

	chunks := make([]string, 0)
	doc.Find("h1, h2, h3, h4, h5, h6, p, li").Each(func(_ int, s *goquery.Selection) {
		text := normalizeWhitespace(s.Text())
		if text != "" {
			chunks = append(chunks, text)
		}
	})
	if len(chunks) > 0 {
		return strings.Join(chunks, " ")
	}

	return normalizeWhitespace(doc.Text())
}

func firstHeadingText(rawHTML string) string {
	if strings.TrimSpace(rawHTML) == "" {
		return ""
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return ""
	}
	for _, selector := range []string{"h1", "h2", "h3", "h4", "h5", "h6"} {
		text := strings.TrimSpace(doc.Find(selector).First().Text())
		if text != "" {
			return normalizeWhitespace(text)
		}
	}
	return ""
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func findSectionMeta(items []SectionIndex, index string) (SectionIndex, bool) {
	index = strings.TrimSpace(index)
	for _, item := range items {
		if item.Index == index {
			return item, true
		}
	}
	return SectionIndex{}, false
}

func coalesce(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
