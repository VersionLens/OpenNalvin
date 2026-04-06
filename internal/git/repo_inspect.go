package git

import (
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/object"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

var (
	ErrRepoEmpty      = errors.New("git repo has no commits")
	ErrBranchNotFound = errors.New("git branch not found")
)

type RepoTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type RepoTree struct {
	RepoName string          `json:"repo_name"`
	Branch   string          `json:"branch"`
	Commit   string          `json:"commit"`
	Entries  []RepoTreeEntry `json:"entries"`
	Lines    []string        `json:"lines"`
}

type RepoCommit struct {
	Hash        string    `json:"hash"`
	ShortHash   string    `json:"short_hash"`
	AuthorName  string    `json:"author_name"`
	AuthorEmail string    `json:"author_email"`
	When        time.Time `json:"when"`
	Subject     string    `json:"subject"`
	Message     string    `json:"message"`
}

type RepoCommitList struct {
	RepoName string       `json:"repo_name"`
	Branch   string       `json:"branch"`
	Head     string       `json:"head"`
	Commits  []RepoCommit `json:"commits"`
}

func InspectRepoTree(cfg configpkg.Config, repoName, branch string) (RepoTree, error) {
	name, repo, err := openManagedRepo(cfg, repoName)
	if err != nil {
		return RepoTree{}, err
	}

	resolvedBranch, commit, err := resolveRepoBranchCommit(repo, branch)
	if err != nil {
		return RepoTree{}, err
	}

	tree, err := commit.Tree()
	if err != nil {
		return RepoTree{}, err
	}

	files, err := committedFiles(tree)
	if err != nil {
		return RepoTree{}, err
	}
	matcher, err := matcherFromTree(files)
	if err != nil {
		return RepoTree{}, err
	}

	kept := make([]string, 0, len(files))
	for _, file := range files {
		if matcher != nil && matcher.Match(strings.Split(file.Name, "/"), false) {
			continue
		}
		kept = append(kept, file.Name)
	}
	sort.Strings(kept)

	entries := make([]RepoTreeEntry, 0, len(kept))
	for _, filePath := range kept {
		entries = append(entries, RepoTreeEntry{
			Path: filePath,
			Type: "file",
		})
	}

	return RepoTree{
		RepoName: name,
		Branch:   resolvedBranch,
		Commit:   commit.Hash.String(),
		Entries:  entries,
		Lines:    renderTreeLines(kept),
	}, nil
}

func ListRepoCommits(cfg configpkg.Config, repoName, branch string) (RepoCommitList, error) {
	name, repo, err := openManagedRepo(cfg, repoName)
	if err != nil {
		return RepoCommitList{}, err
	}

	resolvedBranch, head, err := resolveRepoBranchCommit(repo, branch)
	if err != nil {
		return RepoCommitList{}, err
	}

	iter, err := repo.Log(&gogit.LogOptions{From: head.Hash})
	if err != nil {
		return RepoCommitList{}, err
	}
	defer iter.Close()

	commits := make([]RepoCommit, 0)
	for {
		commit, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return RepoCommitList{}, err
		}

		message := strings.TrimRight(commit.Message, "\n")
		subject := message
		if idx := strings.IndexByte(subject, '\n'); idx >= 0 {
			subject = subject[:idx]
		}
		subject = strings.TrimSpace(subject)

		commits = append(commits, RepoCommit{
			Hash:        commit.Hash.String(),
			ShortHash:   shortHash(commit.Hash.String()),
			AuthorName:  commit.Author.Name,
			AuthorEmail: commit.Author.Email,
			When:        commit.Author.When,
			Subject:     subject,
			Message:     message,
		})
	}

	return RepoCommitList{
		RepoName: name,
		Branch:   resolvedBranch,
		Head:     head.Hash.String(),
		Commits:  commits,
	}, nil
}

func openManagedRepo(cfg configpkg.Config, repoName string) (string, *gogit.Repository, error) {
	name, repoPath, err := RepoPath(cfg, repoName)
	if err != nil {
		return "", nil, err
	}

	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		if errors.Is(err, gogit.ErrRepositoryNotExists) {
			return "", nil, ErrRepoNotFound
		}
		return "", nil, err
	}
	return name, repo, nil
}

func resolveRepoBranchCommit(repo *gogit.Repository, branch string) (string, *object.Commit, error) {
	resolvedBranch, refName, err := resolveRepoBranchName(repo, branch)
	if err != nil {
		return "", nil, err
	}

	ref, err := repo.Reference(refName, true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			if strings.TrimSpace(branch) == "" {
				return "", nil, ErrRepoEmpty
			}
			return "", nil, ErrBranchNotFound
		}
		return "", nil, err
	}
	if ref.Hash().IsZero() {
		return "", nil, ErrRepoEmpty
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return "", nil, ErrRepoEmpty
		}
		return "", nil, err
	}
	return resolvedBranch, commit, nil
}

func resolveRepoBranchName(repo *gogit.Repository, branch string) (string, plumbing.ReferenceName, error) {
	normalized := strings.TrimSpace(branch)
	if normalized != "" {
		normalized = strings.TrimPrefix(normalized, "refs/heads/")
		if normalized == "" || normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "/") {
			return "", "", fmt.Errorf("branch name is invalid")
		}
		refName := plumbing.NewBranchReferenceName(normalized)
		if _, err := repo.Reference(refName, true); err != nil {
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				return "", "", ErrBranchNotFound
			}
			return "", "", err
		}
		return normalized, refName, nil
	}

	branches, err := repo.Branches()
	if err != nil {
		return "", "", err
	}
	defer branches.Close()

	var branchNames []string
	err = branches.ForEach(func(ref *plumbing.Reference) error {
		branchNames = append(branchNames, ref.Name().Short())
		return nil
	})
	if err != nil {
		return "", "", err
	}
	sort.Strings(branchNames)

	headRef, err := repo.Reference(plumbing.HEAD, false)
	if err != nil && !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", "", err
	}

	headTarget := ""
	if err == nil {
		switch {
		case headRef.Type() == plumbing.SymbolicReference && headRef.Target().IsBranch():
			headTarget = headRef.Target().Short()
		case headRef.Name().IsBranch():
			headTarget = headRef.Name().Short()
		}
	}

	if containsString(branchNames, headTarget) {
		return headTarget, plumbing.NewBranchReferenceName(headTarget), nil
	}
	if len(branchNames) == 0 {
		if headTarget != "" {
			return headTarget, plumbing.NewBranchReferenceName(headTarget), nil
		}
		return "", "", ErrRepoEmpty
	}
	if len(branchNames) == 1 {
		return branchNames[0], plumbing.NewBranchReferenceName(branchNames[0]), nil
	}
	if containsString(branchNames, "main") {
		return "main", plumbing.NewBranchReferenceName("main"), nil
	}
	if containsString(branchNames, "master") {
		return "master", plumbing.NewBranchReferenceName("master"), nil
	}
	return branchNames[0], plumbing.NewBranchReferenceName(branchNames[0]), nil
}

func committedFiles(tree *object.Tree) ([]*object.File, error) {
	iter := tree.Files()
	defer iter.Close()

	files := make([]*object.File, 0)
	for {
		file, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func matcherFromTree(files []*object.File) (gitignore.Matcher, error) {
	patterns := make([]gitignore.Pattern, 0)
	for _, file := range files {
		if path.Base(file.Name) != ".gitignore" {
			continue
		}
		content, err := file.Contents()
		if err != nil {
			return nil, err
		}

		dir := path.Dir(file.Name)
		var domain []string
		if dir != "." {
			domain = strings.Split(dir, "/")
		}
		for _, line := range strings.Split(content, "\n") {
			patterns = append(patterns, gitignore.ParsePattern(line, domain))
		}
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	return gitignore.NewMatcher(patterns), nil
}

func renderTreeLines(paths []string) []string {
	root := &treeNode{children: map[string]*treeNode{}}
	for _, filePath := range paths {
		parts := strings.Split(filePath, "/")
		node := root
		for i, part := range parts {
			child, ok := node.children[part]
			if !ok {
				child = &treeNode{name: part, children: map[string]*treeNode{}}
				node.children[part] = child
			}
			if i == len(parts)-1 {
				child.file = true
			}
			node = child
		}
	}

	lines := make([]string, 0, len(paths))
	appendTreeLines(&lines, root, "")
	return lines
}

type treeNode struct {
	name     string
	file     bool
	children map[string]*treeNode
}

func appendTreeLines(lines *[]string, node *treeNode, prefix string) {
	if len(node.children) == 0 {
		return
	}

	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		left := node.children[names[i]]
		right := node.children[names[j]]
		leftDir := len(left.children) > 0 && !left.file
		rightDir := len(right.children) > 0 && !right.file
		if leftDir != rightDir {
			return leftDir
		}
		return names[i] < names[j]
	})

	for i, name := range names {
		child := node.children[name]
		last := i == len(names)-1
		connector := "├── "
		nextPrefix := prefix + "│   "
		if last {
			connector = "└── "
			nextPrefix = prefix + "    "
		}

		label := child.name
		if len(child.children) > 0 && !child.file {
			label += "/"
		}
		*lines = append(*lines, prefix+connector+label)
		appendTreeLines(lines, child, nextPrefix)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
