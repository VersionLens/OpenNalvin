package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	transport "github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	xssh "golang.org/x/crypto/ssh"
)

func ClientAuth(cfg configpkg.Config) (*gitssh.PublicKeys, error) {
	user := strings.TrimSpace(cfg.Git.DefaultClient.User)
	if user == "" {
		return nil, fmt.Errorf("git default client user is required")
	}
	keyPath := strings.TrimSpace(cfg.Git.DefaultClient.PrivateKeyPath)
	if keyPath == "" {
		return nil, fmt.Errorf("git default client private_key_path is required")
	}

	auth, err := gitssh.NewPublicKeysFromFile(user, keyPath, "")
	if err != nil {
		return nil, err
	}

	hostCallback, hostAlgorithms, err := fixedHostKey(cfg.Git.SSH.HostKeyPath)
	if err != nil {
		return nil, err
	}
	auth.HostKeyCallback = hostCallback
	auth.HostKeyAlgorithms = hostAlgorithms
	return auth, nil
}

func CheckoutRepo(cfg configpkg.Config, repoName, destination string) (string, error) {
	url, err := RepoSSHURL(cfg, repoName)
	if err != nil {
		return "", err
	}
	auth, err := ClientAuth(cfg)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", err
	}
	if _, err := gogit.PlainClone(destination, false, &gogit.CloneOptions{
		URL:        url,
		Auth:       auth,
		RemoteName: "origin",
	}); err != nil {
		if err == transport.ErrEmptyRemoteRepository {
			if err := initCheckoutForEmptyRemote(destination, url); err != nil {
				return "", err
			}
			return url, nil
		}
		return "", err
	}

	return url, nil
}

func RepoStatus(repoPath string) (gogit.Status, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return nil, err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return nil, err
	}
	return worktree.Status()
}

func AddPaths(repoPath string, pathspecs []string) error {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}

	for _, pathspec := range pathspecs {
		pathspec = filepath.Clean(filepath.FromSlash(strings.TrimSpace(pathspec)))
		if pathspec == "" || pathspec == "." {
			continue
		}
		if _, err := worktree.Add(pathspec); err != nil {
			return err
		}
	}
	return nil
}

func CommitRepo(cfg configpkg.Config, repoPath, message string) (string, error) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return "", err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return "", err
	}

	authorName := strings.TrimSpace(cfg.Git.DefaultClient.AuthorName)
	if authorName == "" {
		return "", fmt.Errorf("git default client author_name is required")
	}
	authorEmail := strings.TrimSpace(cfg.Git.DefaultClient.AuthorEmail)
	if authorEmail == "" {
		return "", fmt.Errorf("git default client author_email is required")
	}

	hash, err := worktree.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

func PushRepo(cfg configpkg.Config, repoPath, remoteName, branch string) error {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(remoteName) == "" {
		remoteName = "origin"
	}

	if strings.TrimSpace(branch) == "" {
		head, err := repo.Head()
		if err != nil {
			return err
		}
		branch = head.Name().Short()
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("branch is required")
	}

	auth, err := ClientAuth(cfg)
	if err != nil {
		return err
	}

	ref := plumbing.NewBranchReferenceName(branch)
	err = repo.Push(&gogit.PushOptions{
		RemoteName: remoteName,
		Auth:       auth,
		RefSpecs:   []gitcfg.RefSpec{gitcfg.RefSpec(ref + ":" + ref)},
	})
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return err
	}
	return nil
}

func fixedHostKey(privateKeyPath string) (xssh.HostKeyCallback, []string, error) {
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, nil, err
	}
	signer, err := xssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	pub := signer.PublicKey()
	return xssh.FixedHostKey(pub), []string{pub.Type()}, nil
}

func initCheckoutForEmptyRemote(destination, remoteURL string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}

	repo, err := gogit.PlainInit(destination, false)
	if err != nil {
		return err
	}
	_, err = repo.CreateRemote(&gitcfg.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	})
	return err
}
