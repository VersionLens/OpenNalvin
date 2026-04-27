package agent

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
)

var builtinToolKeywords = map[string][]string{
	"search_tools":             {"tool discovery", "find tool", "search tool", "reveal tool", "tool lookup"},
	"shell":                    {"shell script", "compose tools", "pipe", "chain commands", "batch", "scripting", "bash", "pipeline"},
	"spawn_agent":              {"child agent", "subagent", "delegate", "parallel work"},
	"send_input":               {"message child agent", "reply to subagent", "continue child"},
	"wait_agent":               {"wait child agent", "subagent status", "join child"},
	"get_agent_result":         {"child result", "subagent result", "child output", "read child answer"},
	"close_agent":              {"close child agent", "cancel subagent", "stop child"},
	"web_fetch_get":            {"web fetch", "http", "https", "http request", "download url", "web page", "article", "read page", "reader mode", "raw html", "css selector"},
	"wikipedia_get_article":    {"wikipedia", "wiki", "encyclopedia", "article summary", "section index", "wiki section", "research background"},
	"news_rss_headlines":       {"rss", "feed", "feeds", "rss feed", "headlines", "news headlines", "news feed"},
	"news_reddit_top_posts":    {"reddit", "subreddit", "subreddits", "top posts", "reddit top", "reddit news"},
	"news_reddit_post_details": {"reddit", "subreddit", "post details", "post body", "comments", "permalink", "reddit comments"},
	"slack_search":             {"slack", "slack search", "slack messages", "search slack", "workspace messages", "slack channels", "slack conversations"},
	"view":                     {"read file", "view file", "open file", "show file"},
	"ls":                       {"list files", "list directories", "directory tree", "browse workspace"},
	"glob":                     {"find files", "glob files", "match files", "file pattern"},
	"grep":                     {"search text", "grep files", "find in files", "content search"},
	"write":                    {"write file", "create file", "replace file", "save file"},
	"edit":                     {"edit file", "replace text", "update file", "modify file"},
	"multiedit":                {"multiple edits", "batch edit file", "multi edit", "several replacements"},
	"get_workspace_file_url":   {"file url", "serve file", "stream file", "stream video", "vlc", "workspace file url", "media url"},
	"extract_epub":             {"epub", "extract epub", "ebook", "book to markdown", "convert epub", "epub to markdown"},
"git":                      {"git cli", "git status", "git diff", "git add", "git commit", "git push", "git pull", "git fetch", "git branch", "git checkout", "git clone", "git log"},
	"git_list_repos":           {"git repos", "list git repos", "managed repos", "available repos", "clone targets"},
	"git_create_repo":          {"create git repo", "new repo", "initialize managed repo"},
	"git_delete_repo":          {"delete git repo", "remove repo", "destroy repo", "delete managed repo"},
	"git_repo_tree":            {"repo tree", "git tree", "file tree", "list files in repo", "repo contents"},
	"git_repo_commits":         {"repo commits", "git commits", "commit list", "commit history", "git log for repo"},
	"docker":                   {"docker cli", "docker ps", "docker images", "docker build", "docker create", "docker start", "docker stop", "docker rm", "docker exec", "container cli"},
	"docker_list_containers":   {"docker containers", "list containers", "docker ps", "container list", "running containers"},
	"docker_list_images":       {"docker images", "list images", "image list", "available images"},
	"docker_list_networks":     {"docker networks", "list networks", "network list", "container networks"},
	"docker_list_volumes":      {"docker volumes", "list volumes", "volume list", "named volumes"},
	"docker_pull_image":        {"docker pull", "pull image", "download image", "fetch image"},
	"docker_build_image":       {"docker build", "build image", "image build", "dockerfile build"},
	"docker_create_container":  {"docker create", "create container", "new container", "mount workspace", "workspace mount"},
	"docker_start_container":   {"docker start", "start container", "run created container"},
	"docker_stop_container":    {"docker stop", "stop container", "halt container"},
	"docker_remove_container":  {"docker rm", "remove container", "delete container"},
	"docker_exec_foreground":      {"docker exec", "exec container", "run command in container", "container shell", "foreground exec"},
	"docker_exec_background":      {"background process", "start server", "dev server", "long running", "background exec", "daemon process"},
	"docker_exec_tail":            {"tail output", "process logs", "read output", "check server", "background output"},
	"docker_exec_signal":          {"kill process", "stop process", "signal process", "terminate process"},
	"docker_exec_list_processes":  {"list processes", "running processes", "background processes", "ps"},
	"kb_list_nodes":            {"knowledge base", "kb", "list nodes", "search notes", "query nodes"},
	"kb_get_node":              {"knowledge base", "kb", "fetch node", "get node", "read note"},
	"kb_create_node":           {"knowledge base", "kb", "create node", "create note", "new note"},
	"kb_update_node":           {"knowledge base", "kb", "update node", "edit note", "modify note"},
	"kb_delete_node":           {"knowledge base", "kb", "delete node", "remove note"},
	"kb_list_edges":            {"knowledge base", "kb", "graph edge", "relation", "connections"},
	"kb_get_edge":              {"knowledge base", "kb", "fetch edge", "get edge", "relation"},
	"kb_create_edge":           {"knowledge base", "kb", "create edge", "connect nodes", "link relation"},
	"kb_update_edge":           {"knowledge base", "kb", "update edge", "edit relation"},
	"kb_delete_edge":           {"knowledge base", "kb", "delete edge", "remove relation"},
}

var searchStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "for": {}, "from": {}, "help": {}, "i": {}, "if": {}, "in": {},
	"into": {}, "is": {}, "it": {}, "later": {}, "look": {}, "me": {}, "need": {}, "of": {}, "on": {},
	"or": {}, "please": {}, "some": {}, "the": {}, "to": {}, "tool": {}, "tools": {}, "use": {},
	"with": {},
}

type toolSearchDocument struct {
	descriptor ToolDescriptor
	tokenSet   map[string]struct{}
	rawID      string
	idTerms    string
	desc       string
	keywords   string
	serverName string
	schema     string
}

type toolSearchMatch struct {
	document toolSearchDocument
	score    float64
	overlap  int
}

func (rt *agentRuntime) mergedToolKeywords(toolID string) []string {
	metadata, ok := rt.cfg.Agent.Tools.Metadata[toolID]
	if !ok {
		return normalizeKeywordList(builtinToolKeywords[toolID])
	}
	return normalizeKeywordList(builtinToolKeywords[toolID], metadata.Keywords)
}

func (rt *agentRuntime) searchToolDescriptors(query string, limit int) ([]ToolDescriptor, []string, error) {
	hidden := make([]ToolDescriptor, 0)
	for _, descriptor := range rt.catalogResult().Tools {
		if descriptor.Enabled && !descriptor.Visible {
			hidden = append(hidden, descriptor)
		}
	}
	if len(hidden) == 0 || limit <= 0 {
		return nil, nil, nil
	}

	queryTokens := normalizeSearchTokens(query)
	if strings.TrimSpace(query) == "" || len(queryTokens) == 0 {
		if len(hidden) > limit {
			hidden = hidden[:limit]
		}
		return hidden, queryTokens, nil
	}

	if identifierMatches, identifierQuery := matchToolIdentifierQuery(hidden, query, limit); identifierQuery {
		matchedIDs := make([]string, 0, len(identifierMatches))
		for _, descriptor := range identifierMatches {
			matchedIDs = append(matchedIDs, descriptor.ID)
		}
		rt.session.debugf("tool search_tools identifier_query=true matched=%v", matchedIDs)
		return identifierMatches, queryTokens, nil
	}

	rt.session.debugf("tool search_tools normalized_query_tokens=%v", queryTokens)

	documents := make([]toolSearchDocument, 0, len(hidden))
	for _, descriptor := range hidden {
		documents = append(documents, buildToolSearchDocument(descriptor))
	}

	matches, fallbackUsed, err := queryToolSearchIndex(queryTokens, documents)
	if err != nil {
		return nil, queryTokens, err
	}

	minOverlap := minimumToolSearchOverlap(queryTokens)
	filtered := make([]toolSearchMatch, 0, len(matches))
	for _, match := range matches {
		if match.overlap < minOverlap {
			continue
		}
		filtered = append(filtered, match)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].overlap != filtered[j].overlap {
			return filtered[i].overlap > filtered[j].overlap
		}
		if filtered[i].score != filtered[j].score {
			return filtered[i].score < filtered[j].score
		}
		return filtered[i].document.descriptor.ID < filtered[j].document.descriptor.ID
	})

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	descriptors := make([]ToolDescriptor, 0, len(filtered))
	for _, match := range filtered {
		descriptors = append(descriptors, match.document.descriptor)
	}

	matchedIDs := make([]string, 0, len(filtered))
	for _, match := range filtered {
		matchedIDs = append(matchedIDs, fmt.Sprintf("%s(overlap=%d score=%.6f)", match.document.descriptor.ID, match.overlap, match.score))
	}
	rt.session.debugf("tool search_tools fallback=%t matched=%v", fallbackUsed, matchedIDs)

	return descriptors, queryTokens, nil
}

func buildToolSearchDocument(descriptor ToolDescriptor) toolSearchDocument {
	rawID := strings.TrimSpace(descriptor.ID)
	idTerms := tokenizeToolID(rawID)
	desc := strings.TrimSpace(descriptor.Description)
	keywords := strings.Join(descriptor.Keywords, " ")
	serverName := strings.TrimSpace(descriptor.ServerName)
	schemaTerms := collectSchemaSearchTerms(descriptor.Schema)

	tokenSet := make(map[string]struct{})
	for _, token := range normalizeIdentifierSearchTokens(rawID) {
		tokenSet[token] = struct{}{}
	}
	for _, token := range normalizeSearchTokens(idTerms) {
		tokenSet[token] = struct{}{}
	}
	for _, part := range []string{desc, keywords, serverName, schemaTerms} {
		for _, token := range normalizeFreeTextSearchTokens(part) {
			tokenSet[token] = struct{}{}
		}
	}

	return toolSearchDocument{
		descriptor: descriptor,
		tokenSet:   tokenSet,
		rawID:      rawID,
		idTerms:    idTerms,
		desc:       desc,
		keywords:   keywords,
		serverName: serverName,
		schema:     schemaTerms,
	}
}

func queryToolSearchIndex(queryTokens []string, documents []toolSearchDocument) ([]toolSearchMatch, bool, error) {
	db, err := sql.Open("sqlite3", "file:tool-search?mode=memory&cache=private")
	if err != nil {
		return nil, false, fmt.Errorf("open tool search index: %w", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(`
		CREATE VIRTUAL TABLE tools_fts USING fts5(
			tool_ref UNINDEXED,
			raw_id,
			id_terms,
			description,
			keywords,
			server_name,
			schema_terms,
			tokenize = 'unicode61'
		)`); err != nil {
		return nil, false, fmt.Errorf("create tool search index: %w", err)
	}

	stmt, err := db.Prepare(`
		INSERT INTO tools_fts (
			tool_ref, raw_id, id_terms, description, keywords, server_name, schema_terms
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, false, fmt.Errorf("prepare tool search insert: %w", err)
	}
	defer stmt.Close()

	for _, document := range documents {
		if _, err := stmt.Exec(
			document.descriptor.ID,
			document.rawID,
			document.idTerms,
			document.desc,
			document.keywords,
			document.serverName,
			document.schema,
		); err != nil {
			return nil, false, fmt.Errorf("insert tool search document %q: %w", document.descriptor.ID, err)
		}
	}

	results, err := runToolSearchQuery(db, buildToolSearchQuery(queryTokens, "AND"), documents)
	if err != nil {
		return nil, false, err
	}
	if len(results) > 0 {
		return results, false, nil
	}

	results, err = runToolSearchQuery(db, buildToolSearchQuery(queryTokens, "OR"), documents)
	if err != nil {
		return nil, true, err
	}
	return results, true, nil
}

func runToolSearchQuery(db *sql.DB, query string, documents []toolSearchDocument) ([]toolSearchMatch, error) {
	documentByID := make(map[string]toolSearchDocument, len(documents))
	for _, document := range documents {
		documentByID[document.descriptor.ID] = document
	}

	rows, err := db.Query(`
		SELECT tool_ref, bm25(tools_fts, 8.0, 6.0, 4.0, 5.0, 1.0, 2.0) AS score
		FROM tools_fts
		WHERE tools_fts MATCH ?
		ORDER BY score
	`, query)
	if err != nil {
		return nil, fmt.Errorf("query tool search index: %w", err)
	}
	defer rows.Close()

	matches := make([]toolSearchMatch, 0)
	for rows.Next() {
		var (
			toolID string
			score  float64
		)
		if err := rows.Scan(&toolID, &score); err != nil {
			return nil, fmt.Errorf("scan tool search row: %w", err)
		}

		document, ok := documentByID[toolID]
		if !ok {
			continue
		}
		matches = append(matches, toolSearchMatch{
			document: document,
			score:    score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read tool search rows: %w", err)
	}

	for index := range matches {
		matches[index].overlap = countToolSearchOverlap(matches[index].document.tokenSet, normalizeSearchTokens(query))
	}

	return matches, nil
}

func buildToolSearchQuery(tokens []string, operator string) string {
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " "+operator+" ")
}

func minimumToolSearchOverlap(queryTokens []string) int {
	if len(queryTokens) <= 1 {
		return 1
	}
	return 2
}

func countToolSearchOverlap(tokenSet map[string]struct{}, queryTokens []string) int {
	total := 0
	for _, token := range queryTokens {
		if _, ok := tokenSet[token]; ok {
			total++
		}
	}
	return total
}

func tokenizeToolID(toolID string) string {
	replacer := strings.NewReplacer(".", " ", "_", " ", "-", " ")
	return strings.Join(strings.Fields(replacer.Replace(toolID)), " ")
}

func normalizeKeywordList(parts ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, group := range parts {
		for _, keyword := range group {
			keyword = strings.Join(strings.Fields(strings.TrimSpace(keyword)), " ")
			if keyword == "" {
				continue
			}
			key := strings.ToLower(keyword)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, keyword)
		}
	}
	return out
}

func normalizeSearchTokens(value string) []string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	if lowered == "" {
		return nil
	}

	fields := strings.FieldsFunc(lowered, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})

	seen := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		for _, token := range expandSearchTokenVariants(field) {
			if token == "" {
				continue
			}
			if len(token) == 1 && (token[0] < '0' || token[0] > '9') {
				continue
			}
			if _, ok := searchStopWords[token]; ok {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			out = append(out, token)
		}
	}
	return out
}

func normalizeIdentifierSearchTokens(value string) []string {
	return normalizeSearchTokens(value)
}

func normalizeFreeTextSearchTokens(value string) []string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	if lowered == "" {
		return nil
	}

	fields := strings.FieldsFunc(lowered, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})

	seen := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, token := range fields {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if len(token) == 1 && (token[0] < '0' || token[0] > '9') {
			continue
		}
		if _, ok := searchStopWords[token]; ok {
			continue
		}
		if singular := singularizeSearchToken(token); singular != "" && singular != token {
			if _, ok := searchStopWords[singular]; !ok {
				if _, ok := seen[singular]; !ok {
					seen[singular] = struct{}{}
					out = append(out, singular)
				}
			}
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func matchToolIdentifierQuery(descriptors []ToolDescriptor, query string, limit int) ([]ToolDescriptor, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(query))
	if !looksLikeToolIdentifierQuery(trimmed) {
		return nil, false
	}

	matches := make([]ToolDescriptor, 0)
	for _, descriptor := range descriptors {
		normalizedID := strings.ToLower(strings.TrimSpace(descriptor.ID))
		if normalizedID == trimmed || strings.Contains(normalizedID, trimmed) {
			matches = append(matches, descriptor)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(matches[i].ID))
		right := strings.ToLower(strings.TrimSpace(matches[j].ID))
		leftExact := left == trimmed
		rightExact := right == trimmed
		if leftExact != rightExact {
			return leftExact
		}
		if len(left) != len(right) {
			return len(left) < len(right)
		}
		return matches[i].ID < matches[j].ID
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, true
}

func looksLikeToolIdentifierQuery(query string) bool {
	if query == "" || strings.ContainsRune(query, ' ') {
		return false
	}
	return strings.Contains(query, "_") || strings.Contains(query, ".")
}

func expandSearchTokenVariants(token string) []string {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	variants := []string{token}
	if strings.Contains(token, "_") {
		for _, part := range strings.Split(token, "_") {
			part = strings.TrimSpace(part)
			if part != "" {
				variants = append(variants, part)
			}
		}
	}
	if singular := singularizeSearchToken(token); singular != "" && singular != token {
		variants = append(variants, singular)
	}
	return variants
}

func singularizeSearchToken(token string) string {
	switch {
	case len(token) <= 4:
		return ""
	case strings.HasSuffix(token, "ies"):
		return strings.TrimSuffix(token, "ies") + "y"
	case strings.HasSuffix(token, "ses"), strings.HasSuffix(token, "xes"), strings.HasSuffix(token, "zes"):
		return strings.TrimSuffix(token, "es")
	case strings.HasSuffix(token, "s"), !strings.HasSuffix(token, "ss"):
		return strings.TrimSuffix(token, "s")
	default:
		return ""
	}
}

func collectSchemaSearchTerms(schema ToolSchema) string {
	terms := make([]string, 0)
	seen := make(map[string]struct{})
	appendSchemaSearchTerms(&terms, seen, map[string]any{
		"properties": schema.Properties,
		"$defs":      schema.Defs,
	})
	return strings.Join(terms, " ")
}

func appendSchemaSearchTerms(terms *[]string, seen map[string]struct{}, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "properties", "$defs":
				propertyMap, ok := child.(map[string]any)
				if !ok {
					continue
				}
				for name, nested := range propertyMap {
					appendUniqueSearchTerm(terms, seen, name)
					appendSchemaSearchTerms(terms, seen, nested)
				}
			case "items":
				appendSchemaSearchTerms(terms, seen, child)
			case "description", "title":
				if text, ok := child.(string); ok {
					appendUniqueSearchTerm(terms, seen, text)
				}
			case "anyOf", "oneOf", "allOf":
				appendSchemaSearchTerms(terms, seen, child)
			default:
				appendSchemaSearchTerms(terms, seen, child)
			}
		}
	case []any:
		for _, item := range typed {
			appendSchemaSearchTerms(terms, seen, item)
		}
	}
}

func appendUniqueSearchTerm(terms *[]string, seen map[string]struct{}, value string) {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*terms = append(*terms, value)
}
