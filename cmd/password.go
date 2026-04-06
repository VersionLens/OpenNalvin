package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func promptForSecret(in io.Reader, promptOut io.Writer, prompt string) (string, error) {
	if promptOut != nil {
		if _, err := fmt.Fprint(promptOut, prompt); err != nil {
			return "", err
		}
	}

	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		value, err := term.ReadPassword(int(file.Fd()))
		if promptOut != nil {
			_, _ = fmt.Fprintln(promptOut)
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(value)), nil
	}

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if promptOut != nil {
		_, _ = fmt.Fprintln(promptOut)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
