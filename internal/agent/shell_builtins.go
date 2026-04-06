package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	atree "github.com/a8m/tree"
	"github.com/itchyny/gojq"
	"mvdan.cc/sh/v3/interp"
)

// shellBuiltinFn is the signature for a shell builtin implementation.
type shellBuiltinFn func(ctx context.Context, args []string) error

// stdinOrEmpty returns hc.Stdin if non-nil, otherwise an empty reader.
// mvdan/sh can pass a nil Stdin in certain pipeline configurations.
func stdinOrEmpty(hc interp.HandlerContext) io.Reader {
	if hc.Stdin == nil {
		return strings.NewReader("")
	}
	return hc.Stdin
}

// coreBuiltinsWithoutXargs returns the coreutils builtins map, excluding xargs.
// xargs is added separately after the dispatch function is available.
func (rt *agentRuntime) coreBuiltinsWithoutXargs(tmpDir string) map[string]shellBuiltinFn {
	return map[string]shellBuiltinFn{
		"cat":      catBuiltin(tmpDir),
		"head":     headBuiltin,
		"tail":     tailBuiltin,
		"wc":       wcBuiltin,
		"sort":     sortBuiltin,
		"uniq":     uniqBuiltin,
		"fgrep":    fgrepBuiltin(tmpDir),
		"egrep":    egrepBuiltin(tmpDir),
		"tr":       trBuiltin,
		"cut":      cutBuiltin,
		"tee":      teeBuiltin(tmpDir),
		"sed":      sedBuiltin,
		"jq":       jqBuiltin,
		"basename": basenameBuiltin,
		"dirname":  dirnameBuiltin,
		"mkdir":    mkdirBuiltin(tmpDir),
		"rm":       rmBuiltin(tmpDir),
		"cp":       cpBuiltin(tmpDir),
		"mv":       mvBuiltin(tmpDir),
		"touch":    touchBuiltin(tmpDir),
		"seq":      seqBuiltin,
		"sleep":    sleepBuiltin,
		"date":     dateBuiltin,
		"mktemp":   mktempBuiltin(tmpDir),
		"realpath": realpathBuiltin(tmpDir),
		"tree":     treeBuiltin(tmpDir),
		"find":     findBuiltin(tmpDir),
	}
}

// makeXargsBuiltin returns the xargs builtin, which needs the exec dispatch function.
func makeXargsBuiltin(dispatch interp.ExecHandlerFunc) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)

		// Parse options.
		replace := ""    // -I REPLACE placeholder
		maxLines := 0    // -L N or -n N: max lines per invocation (0 = batch all)
		maxArgs := 0     // -n N
		cmdStart := 0    // index in args where the command starts
		nullDelim := false

		i := 0
		for i < len(args) {
			switch args[i] {
			case "-I":
				i++
				if i >= len(args) {
					fmt.Fprintln(hc.Stderr, "xargs: option requires an argument -- 'I'")
					return interp.ExitStatus(1)
				}
				replace = args[i]
			case "-0":
				nullDelim = true
			case "-n", "-L":
				i++
				if i >= len(args) {
					fmt.Fprintf(hc.Stderr, "xargs: option requires an argument -- '%s'\n", args[i-1][1:])
					return interp.ExitStatus(1)
				}
				n, err := strconv.Atoi(args[i])
				if err != nil || n <= 0 {
					fmt.Fprintln(hc.Stderr, "xargs: invalid number")
					return interp.ExitStatus(1)
				}
				if args[i-1] == "-n" {
					maxArgs = n
				} else {
					maxLines = n
				}
			default:
				if strings.HasPrefix(args[i], "-I") {
					replace = args[i][2:]
				} else if strings.HasPrefix(args[i], "-n") {
					n, err := strconv.Atoi(args[i][2:])
					if err != nil || n <= 0 {
						fmt.Fprintln(hc.Stderr, "xargs: invalid number")
						return interp.ExitStatus(1)
					}
					maxArgs = n
				} else if strings.HasPrefix(args[i], "-L") {
					n, err := strconv.Atoi(args[i][2:])
					if err != nil || n <= 0 {
						fmt.Fprintln(hc.Stderr, "xargs: invalid number")
						return interp.ExitStatus(1)
					}
					maxLines = n
				} else {
					cmdStart = i
					goto parsedArgs
				}
			}
			i++
		}
	parsedArgs:
		if cmdStart >= len(args) || (cmdStart == 0 && len(args) == 0) {
			// No command given — echo by default.
			cmdStart = len(args)
		}
		cmdArgs := args[cmdStart:]

		// Read all input lines.
		var lines []string
		var scanner *bufio.Scanner
		if nullDelim {
			scanner = bufio.NewScanner(stdinOrEmpty(hc))
			scanner.Split(splitNull)
		} else {
			scanner = bufio.NewScanner(stdinOrEmpty(hc))
		}
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" || nullDelim {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			return nil
		}

		// Execute command with collected arguments.
		runCmd := func(items []string) error {
			var callArgs []string
			if replace != "" {
				// Replace mode: run once per item.
				return func() error {
					for _, item := range items {
						var call []string
						for _, a := range cmdArgs {
							call = append(call, strings.ReplaceAll(a, replace, item))
						}
						if len(call) == 0 {
							call = []string{item}
						}
						if err := dispatch(ctx, call); err != nil {
							return err
						}
					}
					return nil
				}()
			}
			callArgs = append(callArgs, cmdArgs...)
			callArgs = append(callArgs, items...)
			if len(callArgs) == 0 {
				return nil
			}
			return dispatch(ctx, callArgs)
		}

		if replace != "" {
			for _, line := range lines {
				if err := runCmd([]string{line}); err != nil {
					return err
				}
			}
			return nil
		}

		batchSize := len(lines)
		if maxArgs > 0 {
			batchSize = maxArgs
		} else if maxLines > 0 {
			batchSize = maxLines
		}

		for i := 0; i < len(lines); i += batchSize {
			end := i + batchSize
			if end > len(lines) {
				end = len(lines)
			}
			if err := runCmd(lines[i:end]); err != nil {
				return err
			}
		}
		return nil
	}
}

func splitNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// resolveInTmpDir resolves a path relative to hc.Dir, validates it stays within tmpDir.
func resolveInTmpDir(hc interp.HandlerContext, tmpDir, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(hc.Dir, path)
	}
	path = filepath.Clean(path)
	if !strings.HasPrefix(path, filepath.Clean(tmpDir)+string(filepath.Separator)) &&
		path != filepath.Clean(tmpDir) {
		return "", fmt.Errorf("%s: outside workspace", path)
	}
	return path, nil
}

// --- cat ---

func catBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		if len(args) == 0 {
			_, err := io.Copy(hc.Stdout, stdinOrEmpty(hc))
			return err
		}
		for _, arg := range args {
			path, err := resolveInTmpDir(hc, tmpDir, arg)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "cat: %v\n", err)
				return interp.ExitStatus(1)
			}
			f, err := os.Open(path)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "cat: %s: %v\n", arg, err)
				return interp.ExitStatus(1)
			}
			_, copyErr := io.Copy(hc.Stdout, f)
			f.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		return nil
	}
}

// --- head ---

func headBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	n := 10
	remaining := args
	for len(remaining) > 0 && remaining[0] == "-n" {
		if len(remaining) < 2 {
			fmt.Fprintln(hc.Stderr, "head: option requires an argument -- 'n'")
			return interp.ExitStatus(1)
		}
		val, err := strconv.Atoi(remaining[1])
		if err != nil || val < 0 {
			fmt.Fprintf(hc.Stderr, "head: invalid number of lines: %s\n", remaining[1])
			return interp.ExitStatus(1)
		}
		n = val
		remaining = remaining[2:]
	}
	// Also handle combined -n5 style.
	if len(remaining) > 0 && strings.HasPrefix(remaining[0], "-n") {
		val, err := strconv.Atoi(remaining[0][2:])
		if err == nil && val >= 0 {
			n = val
			remaining = remaining[1:]
		}
	}

	scanner := bufio.NewScanner(stdinOrEmpty(hc))
	for i := 0; i < n && scanner.Scan(); i++ {
		fmt.Fprintln(hc.Stdout, scanner.Text())
	}
	return nil
}

// --- tail ---

func tailBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	n := 10
	fromLine := -1 // if >= 0, print from this line (1-based, +N syntax)
	remaining := args

	for len(remaining) > 0 {
		arg := remaining[0]
		if !strings.HasPrefix(arg, "-n") && arg != "-n" {
			break
		}
		var numStr string
		if arg == "-n" {
			if len(remaining) < 2 {
				fmt.Fprintln(hc.Stderr, "tail: option requires an argument -- 'n'")
				return interp.ExitStatus(1)
			}
			numStr = remaining[1]
			remaining = remaining[2:]
		} else {
			numStr = arg[2:]
			remaining = remaining[1:]
		}
		if strings.HasPrefix(numStr, "+") {
			val, err := strconv.Atoi(numStr[1:])
			if err != nil || val < 1 {
				fmt.Fprintf(hc.Stderr, "tail: invalid number: %s\n", numStr)
				return interp.ExitStatus(1)
			}
			fromLine = val
		} else {
			val, err := strconv.Atoi(numStr)
			if err != nil || val < 0 {
				fmt.Fprintf(hc.Stderr, "tail: invalid number: %s\n", numStr)
				return interp.ExitStatus(1)
			}
			n = val
		}
	}

	// Read all lines into a buffer.
	var lines []string
	scanner := bufio.NewScanner(stdinOrEmpty(hc))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if fromLine >= 0 {
		start := fromLine - 1
		if start > len(lines) {
			start = len(lines)
		}
		for _, l := range lines[start:] {
			fmt.Fprintln(hc.Stdout, l)
		}
	} else {
		start := len(lines) - n
		if start < 0 {
			start = 0
		}
		for _, l := range lines[start:] {
			fmt.Fprintln(hc.Stdout, l)
		}
	}
	return nil
}

// --- wc ---

func wcBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	showLines, showWords, showChars, showBytes := false, false, false, false
	for _, arg := range args {
		switch arg {
		case "-l":
			showLines = true
		case "-w":
			showWords = true
		case "-c":
			showBytes = true
		case "-m":
			showChars = true
		}
	}
	if !showLines && !showWords && !showBytes && !showChars {
		showLines, showWords, showBytes = true, true, true
	}

	data, err := io.ReadAll(stdinOrEmpty(hc))
	if err != nil {
		return err
	}

	lines := bytes.Count(data, []byte("\n"))
	words := len(strings.Fields(string(data)))
	chars := utf8.RuneCount(data)
	byteCount := len(data)

	var parts []string
	if showLines {
		parts = append(parts, strconv.Itoa(lines))
	}
	if showWords {
		parts = append(parts, strconv.Itoa(words))
	}
	if showChars {
		parts = append(parts, strconv.Itoa(chars))
	}
	if showBytes {
		parts = append(parts, strconv.Itoa(byteCount))
	}
	fmt.Fprintln(hc.Stdout, strings.Join(parts, " "))
	return nil
}

// parseLeadingFloat parses the leading numeric value from a line, ignoring any
// trailing non-numeric text (e.g. "100 rfc2616" → 100). This matches the
// behaviour of GNU sort -n.
func parseLeadingFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	// Find the end of the leading numeric token (digits, sign, dot, exponent).
	end := 0
	for end < len(s) {
		c := s[end]
		if c == '-' || c == '+' {
			if end > 0 {
				break
			}
		} else if c != '.' && (c < '0' || c > '9') && c != 'e' && c != 'E' {
			break
		}
		end++
	}
	return strconv.ParseFloat(s[:end], 64)
}

// --- sort ---

func sortBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	reverse, numeric, unique := false, false, false
	for _, arg := range args {
		switch arg {
		case "-r":
			reverse = true
		case "-n":
			numeric = true
		case "-u":
			unique = true
		case "-rn", "-nr":
			reverse = true
			numeric = true
		case "-ru", "-ur":
			reverse = true
			unique = true
		}
	}

	var lines []string
	scanner := bufio.NewScanner(stdinOrEmpty(hc))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	sort.SliceStable(lines, func(i, j int) bool {
		a, b := lines[i], lines[j]
		if numeric {
			na, ea := parseLeadingFloat(a)
			nb, eb := parseLeadingFloat(b)
			if ea == nil && eb == nil {
				if reverse {
					return na > nb
				}
				return na < nb
			}
		}
		if reverse {
			return a > b
		}
		return a < b
	})

	if unique {
		deduped := lines[:0]
		for i, l := range lines {
			if i == 0 || l != lines[i-1] {
				deduped = append(deduped, l)
			}
		}
		lines = deduped
	}

	for _, l := range lines {
		fmt.Fprintln(hc.Stdout, l)
	}
	return nil
}

// --- uniq ---

func uniqBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	count, dupOnly, uniqueOnly := false, false, false
	for _, arg := range args {
		switch arg {
		case "-c":
			count = true
		case "-d":
			dupOnly = true
		case "-u":
			uniqueOnly = true
		}
	}

	type group struct {
		line string
		n    int
	}
	var groups []group
	scanner := bufio.NewScanner(stdinOrEmpty(hc))
	for scanner.Scan() {
		l := scanner.Text()
		if len(groups) > 0 && groups[len(groups)-1].line == l {
			groups[len(groups)-1].n++
		} else {
			groups = append(groups, group{l, 1})
		}
	}

	for _, g := range groups {
		if dupOnly && g.n == 1 {
			continue
		}
		if uniqueOnly && g.n > 1 {
			continue
		}
		if count {
			fmt.Fprintf(hc.Stdout, "%7d %s\n", g.n, g.line)
		} else {
			fmt.Fprintln(hc.Stdout, g.line)
		}
	}
	return nil
}

// --- fgrep / egrep ---

func fgrepBuiltin(tmpDir string) shellBuiltinFn {
	return grepBuiltinImpl(tmpDir, false)
}

func egrepBuiltin(tmpDir string) shellBuiltinFn {
	return grepBuiltinImpl(tmpDir, true)
}

func grepBuiltinImpl(tmpDir string, regex bool) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)

		caseInsensitive := false
		invertMatch := false
		countMode := false
		listFiles := false
		showLineNum := false
		var pattern string
		var fileArgs []string

		i := 0
		for i < len(args) {
			arg := args[i]
			if strings.HasPrefix(arg, "-") && len(arg) > 1 && !strings.HasPrefix(arg, "--") {
				for _, ch := range arg[1:] {
					switch ch {
					case 'i':
						caseInsensitive = true
					case 'v':
						invertMatch = true
					case 'c':
						countMode = true
					case 'l':
						listFiles = true
					case 'n':
						showLineNum = true
					case 'e':
						i++
						if i >= len(args) {
							fmt.Fprintln(hc.Stderr, "grep: option requires an argument -- 'e'")
							return interp.ExitStatus(1)
						}
						pattern = args[i]
					}
				}
			} else if arg == "-e" {
				i++
				if i >= len(args) {
					fmt.Fprintln(hc.Stderr, "grep: option requires an argument -- 'e'")
					return interp.ExitStatus(1)
				}
				pattern = args[i]
			} else {
				if pattern == "" {
					pattern = arg
				} else {
					fileArgs = append(fileArgs, arg)
				}
			}
			i++
		}

		if pattern == "" {
			fmt.Fprintln(hc.Stderr, "grep: missing pattern")
			return interp.ExitStatus(1)
		}

		var re *regexp.Regexp
		if regex {
			pat := pattern
			if caseInsensitive {
				pat = "(?i)" + pat
			}
			var err error
			re, err = regexp.Compile(pat)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "grep: invalid pattern: %v\n", err)
				return interp.ExitStatus(1)
			}
		}

		matchLine := func(line string) bool {
			if regex {
				m := re.MatchString(line)
				return m != invertMatch
			}
			l, p := line, pattern
			if caseInsensitive {
				l, p = strings.ToLower(l), strings.ToLower(p)
			}
			m := strings.Contains(l, p)
			return m != invertMatch
		}

		grepReader := func(r io.Reader, filename string, multiFile bool) (matched bool, err error) {
			scanner := bufio.NewScanner(r)
			lineNum := 0
			matchCount := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Text()
				if matchLine(line) {
					matched = true
					matchCount++
					if listFiles {
						continue
					}
					if !countMode {
						prefix := ""
						if multiFile {
							prefix = filename + ":"
						}
						if showLineNum {
							prefix += strconv.Itoa(lineNum) + ":"
						}
						fmt.Fprintln(hc.Stdout, prefix+line)
					}
				}
			}
			if countMode {
				prefix := ""
				if multiFile {
					prefix = filename + ":"
				}
				fmt.Fprintf(hc.Stdout, "%s%d\n", prefix, matchCount)
			}
			if listFiles && matched {
				fmt.Fprintln(hc.Stdout, filename)
			}
			return matched, scanner.Err()
		}

		if len(fileArgs) == 0 {
			matched, err := grepReader(stdinOrEmpty(hc), "(stdin)", false)
			if err != nil {
				return err
			}
			if !matched {
				return interp.ExitStatus(1)
			}
			return nil
		}

		anyMatch := false
		multiFile := len(fileArgs) > 1
		for _, fileArg := range fileArgs {
			path, err := resolveInTmpDir(hc, tmpDir, fileArg)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "grep: %v\n", err)
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "grep: %s: %v\n", fileArg, err)
				continue
			}
			m, grepErr := grepReader(f, fileArg, multiFile)
			f.Close()
			if grepErr != nil {
				fmt.Fprintf(hc.Stderr, "grep: %s: %v\n", fileArg, grepErr)
			}
			if m {
				anyMatch = true
			}
		}
		if !anyMatch {
			return interp.ExitStatus(1)
		}
		return nil
	}
}

// --- tr ---

func trBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	deleteMode := false
	squeezeMode := false

	remaining := args
	for len(remaining) > 0 && strings.HasPrefix(remaining[0], "-") {
		switch remaining[0] {
		case "-d":
			deleteMode = true
		case "-s":
			squeezeMode = true
		case "-ds", "-sd":
			deleteMode = true
			squeezeMode = true
		}
		remaining = remaining[1:]
	}

	if len(remaining) == 0 {
		_, err := io.Copy(hc.Stdout, stdinOrEmpty(hc))
		return err
	}

	set1 := expandTrSet(remaining[0])
	var set2 []rune
	if len(remaining) > 1 {
		set2 = expandTrSet(remaining[1])
	}

	buildTable := func() map[rune]rune {
		table := make(map[rune]rune)
		for i, r := range set1 {
			if i < len(set2) {
				table[r] = set2[i]
			} else if len(set2) > 0 {
				table[r] = set2[len(set2)-1]
			}
		}
		return table
	}

	if deleteMode {
		deleteSet := make(map[rune]bool)
		for _, r := range set1 {
			deleteSet[r] = true
		}
		data, err := io.ReadAll(stdinOrEmpty(hc))
		if err != nil {
			return err
		}
		var out strings.Builder
		for _, r := range string(data) {
			if !deleteSet[r] {
				out.WriteRune(r)
			}
		}
		_, err = fmt.Fprint(hc.Stdout, out.String())
		return err
	}

	table := buildTable()
	data, err := io.ReadAll(stdinOrEmpty(hc))
	if err != nil {
		return err
	}

	var out strings.Builder
	var lastOut rune = -1
	for _, r := range string(data) {
		mapped := r
		if m, ok := table[r]; ok {
			mapped = m
		}
		if squeezeMode && mapped == lastOut && contains(set2, mapped) {
			continue
		}
		out.WriteRune(mapped)
		lastOut = mapped
	}
	_, err = fmt.Fprint(hc.Stdout, out.String())
	return err
}

func expandTrSet(s string) []rune {
	var result []rune
	// Handle character classes.
	if strings.HasPrefix(s, "[:") && strings.HasSuffix(s, ":]") {
		class := s[2 : len(s)-2]
		switch class {
		case "alpha":
			for r := 'a'; r <= 'z'; r++ {
				result = append(result, r)
			}
			for r := 'A'; r <= 'Z'; r++ {
				result = append(result, r)
			}
			return result
		case "digit":
			for r := '0'; r <= '9'; r++ {
				result = append(result, r)
			}
			return result
		case "lower":
			for r := 'a'; r <= 'z'; r++ {
				result = append(result, r)
			}
			return result
		case "upper":
			for r := 'A'; r <= 'Z'; r++ {
				result = append(result, r)
			}
			return result
		case "space":
			return []rune{' ', '\t', '\n', '\r', '\f', '\v'}
		}
	}

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if i+2 < len(runes) && runes[i+1] == '-' {
			from, to := runes[i], runes[i+2]
			if from <= to {
				for r := from; r <= to; r++ {
					result = append(result, r)
				}
				i += 2
				continue
			}
		}
		result = append(result, runes[i])
	}
	return result
}

func contains(runes []rune, r rune) bool {
	for _, v := range runes {
		if v == r {
			return true
		}
	}
	return false
}

// --- cut ---

func cutBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	delim := "\t"
	var fields []int

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-d":
			i++
			if i >= len(args) {
				fmt.Fprintln(hc.Stderr, "cut: option requires an argument -- 'd'")
				return interp.ExitStatus(1)
			}
			delim = args[i]
		case "-f":
			i++
			if i >= len(args) {
				fmt.Fprintln(hc.Stderr, "cut: option requires an argument -- 'f'")
				return interp.ExitStatus(1)
			}
			var err error
			fields, err = parseFieldSpec(args[i])
			if err != nil {
				fmt.Fprintf(hc.Stderr, "cut: %v\n", err)
				return interp.ExitStatus(1)
			}
		default:
			if strings.HasPrefix(args[i], "-d") {
				delim = args[i][2:]
			} else if strings.HasPrefix(args[i], "-f") {
				var err error
				fields, err = parseFieldSpec(args[i][2:])
				if err != nil {
					fmt.Fprintf(hc.Stderr, "cut: %v\n", err)
					return interp.ExitStatus(1)
				}
			}
		}
	}

	fieldSet := make(map[int]bool)
	for _, f := range fields {
		fieldSet[f] = true
	}

	scanner := bufio.NewScanner(stdinOrEmpty(hc))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, delim)
		var out []string
		for i, part := range parts {
			if fieldSet[i+1] {
				out = append(out, part)
			}
		}
		fmt.Fprintln(hc.Stdout, strings.Join(out, delim))
	}
	return nil
}

func parseFieldSpec(spec string) ([]int, error) {
	var fields []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 != nil || err2 != nil || start < 1 || end < start {
				return nil, fmt.Errorf("invalid field spec: %s", part)
			}
			for f := start; f <= end; f++ {
				fields = append(fields, f)
			}
		} else {
			f, err := strconv.Atoi(part)
			if err != nil || f < 1 {
				return nil, fmt.Errorf("invalid field spec: %s", part)
			}
			fields = append(fields, f)
		}
	}
	return fields, nil
}

// --- tee ---

func teeBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		appendMode := false
		var fileArgs []string

		for _, arg := range args {
			if arg == "-a" {
				appendMode = true
			} else {
				fileArgs = append(fileArgs, arg)
			}
		}

		writers := []io.Writer{hc.Stdout}
		var filesToClose []io.Closer
		defer func() {
			for _, f := range filesToClose {
				f.Close()
			}
		}()

		for _, fileArg := range fileArgs {
			path, err := resolveInTmpDir(hc, tmpDir, fileArg)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "tee: %v\n", err)
				return interp.ExitStatus(1)
			}
			flag := os.O_CREATE | os.O_WRONLY
			if appendMode {
				flag |= os.O_APPEND
			} else {
				flag |= os.O_TRUNC
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				fmt.Fprintf(hc.Stderr, "tee: %s: %v\n", fileArg, err)
				return interp.ExitStatus(1)
			}
			f, err := os.OpenFile(path, flag, 0o644)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "tee: %s: %v\n", fileArg, err)
				return interp.ExitStatus(1)
			}
			writers = append(writers, f)
			filesToClose = append(filesToClose, f)
		}

		_, err := io.Copy(io.MultiWriter(writers...), stdinOrEmpty(hc))
		return err
	}
}

// --- sed ---
// Supports: s/old/new/[g], /pattern/d (delete matching lines)

func sedBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	var expressions []string

	for i := 0; i < len(args); i++ {
		if args[i] == "-e" {
			i++
			if i >= len(args) {
				fmt.Fprintln(hc.Stderr, "sed: option requires an argument -- 'e'")
				return interp.ExitStatus(1)
			}
			expressions = append(expressions, args[i])
		} else if strings.HasPrefix(args[i], "-e") {
			expressions = append(expressions, args[i][2:])
		} else if !strings.HasPrefix(args[i], "-") {
			expressions = append(expressions, args[i])
		}
	}

	type sedOp struct {
		delete  bool
		matchRe *regexp.Regexp
		subFrom *regexp.Regexp
		subTo   string
		global  bool
	}

	var ops []sedOp
	for _, expr := range expressions {
		op := sedOp{}
		if strings.HasPrefix(expr, "s") && len(expr) > 1 {
			sep := string(expr[1])
			parts := strings.Split(expr[2:], sep)
			if len(parts) >= 2 {
				flags := ""
				if len(parts) >= 3 {
					flags = parts[2]
				}
				pattern := parts[0]
				replacement := parts[1]
				re, err := regexp.Compile(pattern)
				if err != nil {
					fmt.Fprintf(hc.Stderr, "sed: invalid pattern: %v\n", err)
					return interp.ExitStatus(1)
				}
				op.subFrom = re
				op.subTo = replacement
				op.global = strings.Contains(flags, "g")
				ops = append(ops, op)
			}
		} else if strings.HasSuffix(expr, "d") && len(expr) > 1 {
			patternPart := expr[:len(expr)-1]
			sep := string(patternPart[0])
			inner := strings.Trim(patternPart, sep)
			re, err := regexp.Compile(inner)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "sed: invalid pattern: %v\n", err)
				return interp.ExitStatus(1)
			}
			op.delete = true
			op.matchRe = re
			ops = append(ops, op)
		}
	}

	scanner := bufio.NewScanner(stdinOrEmpty(hc))
	for scanner.Scan() {
		line := scanner.Text()
		skip := false
		for _, op := range ops {
			if op.delete {
				if op.matchRe.MatchString(line) {
					skip = true
					break
				}
			} else if op.subFrom != nil {
				if op.global {
					line = op.subFrom.ReplaceAllString(line, op.subTo)
				} else {
					replaced := false
					line = op.subFrom.ReplaceAllStringFunc(line, func(s string) string {
						if !replaced {
							replaced = true
							return op.subFrom.ReplaceAllString(s, op.subTo)
						}
						return s
					})
				}
			}
		}
		if !skip {
			fmt.Fprintln(hc.Stdout, line)
		}
	}
	return nil
}

// --- jq ---

func jqBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)

	rawOutput := false
	compactOutput := false
	nullInput := false
	joinOutput := false
	filterStr := "."
	var filterIdx int = -1

	for i, arg := range args {
		switch arg {
		case "-r", "--raw-output":
			rawOutput = true
		case "-c", "--compact-output":
			compactOutput = true
		case "-n", "--null-input":
			nullInput = true
		case "-j", "--join-output":
			joinOutput = true
		case "-R", "--raw-input":
			// treat each line as a raw string
		default:
			if !strings.HasPrefix(arg, "-") && filterIdx < 0 {
				filterStr = arg
				filterIdx = i
			}
		}
	}

	query, err := gojq.Parse(filterStr)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "jq: %v\n", err)
		return interp.ExitStatus(1)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "jq: %v\n", err)
		return interp.ExitStatus(1)
	}

	runFilter := func(input any) error {
		iter := code.Run(input)
		for {
			v, ok := iter.Next()
			if !ok {
				break
			}
			if errV, ok := v.(error); ok {
				fmt.Fprintf(hc.Stderr, "jq: %v\n", errV)
				return interp.ExitStatus(5)
			}

			var out string
			if rawOutput {
				if s, ok := v.(string); ok {
					out = s
				} else {
					b, _ := json.Marshal(v)
					out = string(b)
				}
			} else if compactOutput {
				b, _ := json.Marshal(v)
				out = string(b)
			} else {
				b, _ := json.MarshalIndent(v, "", "  ")
				out = string(b)
			}

			if joinOutput {
				fmt.Fprint(hc.Stdout, out)
			} else {
				fmt.Fprintln(hc.Stdout, out)
			}
		}
		return nil
	}

	if nullInput {
		return runFilter(nil)
	}

	// Stream-parse multiple JSON values from stdin (ndjson).
	dec := json.NewDecoder(stdinOrEmpty(hc))
	hasInput := false
	for dec.More() {
		hasInput = true
		var input any
		if err := dec.Decode(&input); err != nil {
			fmt.Fprintf(hc.Stderr, "jq: invalid JSON input: %v\n", err)
			return interp.ExitStatus(1)
		}
		if err := runFilter(input); err != nil {
			return err
		}
	}
	if !hasInput {
		return runFilter(nil)
	}
	return nil
}

// --- basename / dirname ---

func basenameBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	if len(args) == 0 {
		fmt.Fprintln(hc.Stderr, "basename: missing operand")
		return interp.ExitStatus(1)
	}
	result := filepath.Base(args[0])
	if len(args) >= 2 {
		result = strings.TrimSuffix(result, args[1])
	}
	fmt.Fprintln(hc.Stdout, result)
	return nil
}

func dirnameBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	if len(args) == 0 {
		fmt.Fprintln(hc.Stderr, "dirname: missing operand")
		return interp.ExitStatus(1)
	}
	fmt.Fprintln(hc.Stdout, filepath.Dir(args[0]))
	return nil
}

// --- mkdir ---

func mkdirBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		parents := false
		var dirs []string
		for _, arg := range args {
			if arg == "-p" {
				parents = true
			} else {
				dirs = append(dirs, arg)
			}
		}
		for _, d := range dirs {
			path, err := resolveInTmpDir(hc, tmpDir, d)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "mkdir: %v\n", err)
				return interp.ExitStatus(1)
			}
			if parents {
				if err := os.MkdirAll(path, 0o755); err != nil {
					fmt.Fprintf(hc.Stderr, "mkdir: %s: %v\n", d, err)
					return interp.ExitStatus(1)
				}
			} else {
				if err := os.Mkdir(path, 0o755); err != nil {
					fmt.Fprintf(hc.Stderr, "mkdir: %s: %v\n", d, err)
					return interp.ExitStatus(1)
				}
			}
		}
		return nil
	}
}

// --- rm ---

func rmBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		recursive := false
		force := false
		var targets []string

		for _, arg := range args {
			switch arg {
			case "-r", "-R", "--recursive":
				recursive = true
			case "-f", "--force":
				force = true
			case "-rf", "-fr", "-Rf", "-fR":
				recursive = true
				force = true
			default:
				targets = append(targets, arg)
			}
		}

		for _, t := range targets {
			path, err := resolveInTmpDir(hc, tmpDir, t)
			if err != nil {
				if !force {
					fmt.Fprintf(hc.Stderr, "rm: %v\n", err)
					return interp.ExitStatus(1)
				}
				continue
			}
			if recursive {
				err = os.RemoveAll(path)
			} else {
				err = os.Remove(path)
			}
			if err != nil && !force {
				fmt.Fprintf(hc.Stderr, "rm: %s: %v\n", t, err)
				return interp.ExitStatus(1)
			}
		}
		return nil
	}
}

// --- cp ---

func cpBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		recursive := false
		var operands []string

		for _, arg := range args {
			switch arg {
			case "-r", "-R", "--recursive":
				recursive = true
			default:
				operands = append(operands, arg)
			}
		}

		if len(operands) < 2 {
			fmt.Fprintln(hc.Stderr, "cp: missing operand")
			return interp.ExitStatus(1)
		}

		dst, err := resolveInTmpDir(hc, tmpDir, operands[len(operands)-1])
		if err != nil {
			fmt.Fprintf(hc.Stderr, "cp: %v\n", err)
			return interp.ExitStatus(1)
		}

		srcs := operands[:len(operands)-1]
		for _, src := range srcs {
			srcPath, err := resolveInTmpDir(hc, tmpDir, src)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "cp: %v\n", err)
				return interp.ExitStatus(1)
			}

			info, err := os.Stat(srcPath)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "cp: %s: %v\n", src, err)
				return interp.ExitStatus(1)
			}

			dstPath := dst
			dstInfo, dstErr := os.Stat(dst)
			if dstErr == nil && dstInfo.IsDir() {
				dstPath = filepath.Join(dst, filepath.Base(srcPath))
			}

			if info.IsDir() {
				if !recursive {
					fmt.Fprintf(hc.Stderr, "cp: %s: is a directory (use -r)\n", src)
					return interp.ExitStatus(1)
				}
				if err := copyDir(srcPath, dstPath); err != nil {
					fmt.Fprintf(hc.Stderr, "cp: %v\n", err)
					return interp.ExitStatus(1)
				}
			} else {
				if err := copyFile(srcPath, dstPath); err != nil {
					fmt.Fprintf(hc.Stderr, "cp: %v\n", err)
					return interp.ExitStatus(1)
				}
			}
		}
		return nil
	}
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// --- mv ---

func mvBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		var operands []string
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") {
				operands = append(operands, arg)
			}
		}

		if len(operands) < 2 {
			fmt.Fprintln(hc.Stderr, "mv: missing operand")
			return interp.ExitStatus(1)
		}

		dst, err := resolveInTmpDir(hc, tmpDir, operands[len(operands)-1])
		if err != nil {
			fmt.Fprintf(hc.Stderr, "mv: %v\n", err)
			return interp.ExitStatus(1)
		}

		srcs := operands[:len(operands)-1]
		for _, src := range srcs {
			srcPath, err := resolveInTmpDir(hc, tmpDir, src)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "mv: %v\n", err)
				return interp.ExitStatus(1)
			}

			dstPath := dst
			dstInfo, dstErr := os.Stat(dst)
			if dstErr == nil && dstInfo.IsDir() {
				dstPath = filepath.Join(dst, filepath.Base(srcPath))
			}

			if err := os.Rename(srcPath, dstPath); err != nil {
				fmt.Fprintf(hc.Stderr, "mv: %s: %v\n", src, err)
				return interp.ExitStatus(1)
			}
		}
		return nil
	}
}

// --- touch ---

func touchBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		var files []string
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") {
				files = append(files, arg)
			}
		}
		now := time.Now()
		for _, file := range files {
			path, err := resolveInTmpDir(hc, tmpDir, file)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "touch: %v\n", err)
				return interp.ExitStatus(1)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				fmt.Fprintf(hc.Stderr, "touch: %s: %v\n", file, err)
				return interp.ExitStatus(1)
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "touch: %s: %v\n", file, err)
				return interp.ExitStatus(1)
			}
			f.Close()
			if err := os.Chtimes(path, now, now); err != nil {
				fmt.Fprintf(hc.Stderr, "touch: %s: %v\n", file, err)
			}
		}
		return nil
	}
}

// --- seq ---

func seqBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	var first, incr, last float64
	var format string

	nonFlags := args
	if len(nonFlags) == 1 {
		n, err := strconv.ParseFloat(nonFlags[0], 64)
		if err != nil {
			fmt.Fprintf(hc.Stderr, "seq: invalid number: %s\n", nonFlags[0])
			return interp.ExitStatus(1)
		}
		first, incr, last = 1, 1, n
	} else if len(nonFlags) == 2 {
		a, err1 := strconv.ParseFloat(nonFlags[0], 64)
		b, err2 := strconv.ParseFloat(nonFlags[1], 64)
		if err1 != nil || err2 != nil {
			fmt.Fprintln(hc.Stderr, "seq: invalid number")
			return interp.ExitStatus(1)
		}
		first, incr, last = a, 1, b
	} else if len(nonFlags) >= 3 {
		a, err1 := strconv.ParseFloat(nonFlags[0], 64)
		b, err2 := strconv.ParseFloat(nonFlags[1], 64)
		c, err3 := strconv.ParseFloat(nonFlags[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			fmt.Fprintln(hc.Stderr, "seq: invalid number")
			return interp.ExitStatus(1)
		}
		first, incr, last = a, b, c
	} else {
		fmt.Fprintln(hc.Stderr, "seq: missing operand")
		return interp.ExitStatus(1)
	}

	if incr == 0 {
		fmt.Fprintln(hc.Stderr, "seq: zero increment")
		return interp.ExitStatus(1)
	}

	// Determine output format: integer if all values are integers.
	allInt := first == math.Trunc(first) && incr == math.Trunc(incr) && last == math.Trunc(last)
	if format == "" {
		if allInt {
			format = "%d"
		} else {
			format = "%g"
		}
	}

	for v := first; (incr > 0 && v <= last) || (incr < 0 && v >= last); v += incr {
		if allInt {
			fmt.Fprintf(hc.Stdout, "%d\n", int64(v))
		} else {
			fmt.Fprintf(hc.Stdout, format+"\n", v)
		}
	}
	return nil
}

// --- sleep ---

func sleepBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	if len(args) == 0 {
		fmt.Fprintln(hc.Stderr, "sleep: missing operand")
		return interp.ExitStatus(1)
	}
	n, err := strconv.ParseFloat(args[0], 64)
	if err != nil || n < 0 {
		fmt.Fprintf(hc.Stderr, "sleep: invalid time interval: %s\n", args[0])
		return interp.ExitStatus(1)
	}
	select {
	case <-time.After(time.Duration(n * float64(time.Second))):
	case <-ctx.Done():
		return interp.ExitStatus(130)
	}
	return nil
}

// --- date ---

func dateBuiltin(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	now := time.Now()
	format := now.Format("Mon Jan  2 15:04:05 MST 2006")

	for _, arg := range args {
		if strings.HasPrefix(arg, "+") {
			// Convert strftime-like format to Go layout.
			format = convertDateFormat(arg[1:], now)
		}
	}
	fmt.Fprintln(hc.Stdout, format)
	return nil
}

func convertDateFormat(fmt string, t time.Time) string {
	// Map common strftime codes to Go time layout equivalents.
	replacements := [][2]string{
		{"%Y", "2006"}, {"%m", "01"}, {"%d", "02"},
		{"%H", "15"}, {"%M", "04"}, {"%S", "05"},
		{"%Z", "MST"}, {"%z", "-0700"},
		{"%A", "Monday"}, {"%a", "Mon"},
		{"%B", "January"}, {"%b", "Jan"},
		{"%e", " 2"}, {"%j", "002"},
		{"%n", "\n"}, {"%t", "\t"},
		{"%%", "%"},
	}
	result := fmt
	for _, r := range replacements {
		result = strings.ReplaceAll(result, r[0], r[1])
	}
	return t.Format(result)
}

// --- mktemp ---

func mktempBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		dirMode := false
		for _, arg := range args {
			if arg == "-d" {
				dirMode = true
			}
		}

		var path string
		var err error
		if dirMode {
			path, err = os.MkdirTemp(tmpDir, "tmp.*")
		} else {
			var f *os.File
			f, err = os.CreateTemp(tmpDir, "tmp.*")
			if err == nil {
				path = f.Name()
				f.Close()
			}
		}
		if err != nil {
			fmt.Fprintf(hc.Stderr, "mktemp: %v\n", err)
			return interp.ExitStatus(1)
		}

		// Return workspace-relative path (relative to tmpDir).
		rel, err := filepath.Rel(tmpDir, path)
		if err != nil {
			rel = path
		}
		fmt.Fprintln(hc.Stdout, rel)
		return nil
	}
}

// --- realpath ---

func realpathBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		if len(args) == 0 {
			fmt.Fprintln(hc.Stderr, "realpath: missing operand")
			return interp.ExitStatus(1)
		}
		for _, arg := range args {
			path, err := resolveInTmpDir(hc, tmpDir, arg)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "realpath: %v\n", err)
				return interp.ExitStatus(1)
			}
			// Return relative to tmpDir.
			rel, err := filepath.Rel(tmpDir, path)
			if err != nil {
				rel = path
			}
			fmt.Fprintln(hc.Stdout, rel)
		}
		return nil
	}
}

// --- tree ---

// tmpDirFS implements atree.Fs by translating tree's absolute paths back to
// real tmpDir paths. tree.New receives a display path (workspace-relative),
// so Stat/ReadDir calls arrive with that display path as prefix; we replace
// it with the real tmpDir prefix before hitting the OS.
type tmpDirFS struct {
	displayRoot string // path passed to tree.New, e.g. "." or "downloads"
	realRoot    string // corresponding absolute path inside tmpDir
}

func (f *tmpDirFS) Stat(path string) (os.FileInfo, error) {
	return os.Lstat(f.real(path))
}

func (f *tmpDirFS) ReadDir(path string) ([]string, error) {
	dir, err := os.Open(f.real(path))
	if err != nil {
		return nil, err
	}
	names, err := dir.Readdirnames(-1)
	dir.Close()
	return names, err
}

func (f *tmpDirFS) real(path string) string {
	// tree constructs child paths as filepath.Join(parentPath, name), so
	// paths always have f.displayRoot as a prefix.
	rel, err := filepath.Rel(f.displayRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path // fallback: shouldn't happen
	}
	return filepath.Join(f.realRoot, rel)
}

func treeBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)

		depth := 0    // 0 = unlimited
		dirsOnly := false
		all := false
		noReport := false
		pathArg := "."

		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-L":
				if i+1 < len(args) {
					i++
					n, err := strconv.Atoi(args[i])
					if err != nil || n < 1 {
						fmt.Fprintf(hc.Stderr, "tree: invalid level '%s'\n", args[i])
						return interp.ExitStatus(1)
					}
					depth = n
				}
			case "-d":
				dirsOnly = true
			case "-a":
				all = true
			case "--noreport":
				noReport = true
			default:
				if !strings.HasPrefix(args[i], "-") {
					pathArg = args[i]
				}
			}
		}

		absPath, err := resolveInTmpDir(hc, tmpDir, pathArg)
		if err != nil {
			fmt.Fprintf(hc.Stderr, "tree: %v\n", err)
			return interp.ExitStatus(1)
		}

		// displayPath is the label shown at the tree root (workspace-relative).
		// We clean it so "." stays "." and "downloads" stays "downloads".
		displayPath := filepath.Clean(pathArg)

		opts := &atree.Options{
			Fs: &tmpDirFS{
				displayRoot: displayPath,
				realRoot:    absPath,
			},
			OutFile:   hc.Stdout,
			All:       all,
			DirsOnly:  dirsOnly,
			DeepLevel: depth,
		}

		inf := atree.New(displayPath)
		nd, nf := inf.Visit(opts)
		inf.Print(opts)

		if !noReport {
			dirs := "directories"
			if nd == 1 {
				dirs = "directory"
			}
			if dirsOnly {
				fmt.Fprintf(hc.Stdout, "\n%d %s\n", nd, dirs)
			} else {
				files := "files"
				if nf == 1 {
					files = "file"
				}
				fmt.Fprintf(hc.Stdout, "\n%d %s, %d %s\n", nd, dirs, nf, files)
			}
		}

		return nil
	}
}

// --- find ---

// findBuiltin implements a subset of POSIX find:
//
//	find [path...] [predicates]
//
// Supported predicates (evaluated with implicit AND):
//
//	-name pattern        glob match on filename (case-sensitive)
//	-iname pattern       glob match on filename (case-insensitive)
//	-path pattern        glob match on full path (e.g. "*/downloads/*")
//	-ipath pattern       glob match on full path (case-insensitive)
//	-type f|d|l          file type: regular file, directory, symlink
//	-maxdepth N          descend at most N directory levels below start
//	-mindepth N          skip entries shallower than N levels
//	-size [+|-]N[ckMG]   file size comparison (c=bytes k=KiB M=MiB G=GiB)
//	-not pred / ! pred   negate the immediately following predicate
//	-o                   OR — separates two AND-groups (lower precedence)
//	-print               no-op; print is the default action
//	-print0              NUL-delimited output instead of newlines
func findBuiltin(tmpDir string) shellBuiltinFn {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)

		// Collect leading path arguments (everything before the first flag or !).
		var startPaths []string
		i := 0
		for i < len(args) && !strings.HasPrefix(args[i], "-") && args[i] != "!" {
			startPaths = append(startPaths, args[i])
			i++
		}
		if len(startPaths) == 0 {
			startPaths = []string{"."}
		}
		tokens := args[i:]

		// findCond evaluates a single predicate for a file entry.
		type findCond func(relPath string, d fs.DirEntry, depth int) (bool, error)

		// parseCond consumes one condition (possibly prefixed by -not/!) from
		// tokens[pos:] and returns the condition and the next token index.
		var parseCond func(tokens []string, pos int) (findCond, int, error)
		parseCond = func(tokens []string, pos int) (findCond, int, error) {
			if pos >= len(tokens) {
				return nil, pos, fmt.Errorf("find: expected predicate")
			}
			tok := tokens[pos]

			if tok == "-not" || tok == "!" {
				inner, next, err := parseCond(tokens, pos+1)
				if err != nil {
					return nil, next, err
				}
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					v, err := inner(p, d, depth)
					return !v, err
				}, next, nil
			}

			switch tok {
			case "-name", "-iname":
				if pos+1 >= len(tokens) {
					return nil, pos, fmt.Errorf("find: %s needs argument", tok)
				}
				pattern := tokens[pos+1]
				ci := tok == "-iname"
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					name := filepath.Base(p)
					pat := pattern
					if ci {
						name = strings.ToLower(name)
						pat = strings.ToLower(pat)
					}
					return filepath.Match(pat, name)
				}, pos + 2, nil

			case "-path", "-ipath":
				if pos+1 >= len(tokens) {
					return nil, pos, fmt.Errorf("find: %s needs argument", tok)
				}
				pattern := tokens[pos+1]
				ci := tok == "-ipath"
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					path := p
					pat := pattern
					if ci {
						path = strings.ToLower(path)
						pat = strings.ToLower(pat)
					}
					// Support both exact match and leading ./ variants.
					ok, err := filepath.Match(pat, path)
					if !ok && err == nil {
						// Also try matching against ./path for patterns like "*/downloads/*".
						ok, err = filepath.Match(pat, "./"+path)
					}
					return ok, err
				}, pos + 2, nil

			case "-type":
				if pos+1 >= len(tokens) {
					return nil, pos, fmt.Errorf("find: -type needs argument")
				}
				typ := tokens[pos+1]
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					switch typ {
					case "f":
						return d.Type().IsRegular(), nil
					case "d":
						return d.IsDir(), nil
					case "l":
						return d.Type()&fs.ModeSymlink != 0, nil
					}
					return false, fmt.Errorf("find: unknown -type %q", typ)
				}, pos + 2, nil

			case "-maxdepth":
				if pos+1 >= len(tokens) {
					return nil, pos, fmt.Errorf("find: -maxdepth needs argument")
				}
				n, err := strconv.Atoi(tokens[pos+1])
				if err != nil {
					return nil, pos, fmt.Errorf("find: -maxdepth: %v", err)
				}
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					return depth <= n, nil
				}, pos + 2, nil

			case "-mindepth":
				if pos+1 >= len(tokens) {
					return nil, pos, fmt.Errorf("find: -mindepth needs argument")
				}
				n, err := strconv.Atoi(tokens[pos+1])
				if err != nil {
					return nil, pos, fmt.Errorf("find: -mindepth: %v", err)
				}
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					return depth >= n, nil
				}, pos + 2, nil

			case "-size":
				if pos+1 >= len(tokens) {
					return nil, pos, fmt.Errorf("find: -size needs argument")
				}
				cmp, bytes, err := parseFindSize(tokens[pos+1])
				if err != nil {
					return nil, pos, fmt.Errorf("find: -size: %v", err)
				}
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					info, err := d.Info()
					if err != nil {
						return false, nil
					}
					sz := info.Size()
					switch cmp {
					case '+':
						return sz > bytes, nil
					case '-':
						return sz < bytes, nil
					default:
						return sz == bytes, nil
					}
				}, pos + 2, nil

			case "-print", "-print0":
				return func(p string, d fs.DirEntry, depth int) (bool, error) {
					return true, nil
				}, pos + 1, nil

			default:
				return nil, pos, fmt.Errorf("find: unknown predicate %q", tok)
			}
		}

		print0 := false
		for _, t := range tokens {
			if t == "-print0" {
				print0 = true
			}
		}

		// Split tokens on -o into AND-groups, then parse each group.
		type andGroup []findCond
		var orGroups []andGroup

		grpTokens := []string{}
		for _, t := range tokens {
			if t == "-o" {
				var conds andGroup
				pos := 0
				for pos < len(grpTokens) {
					if grpTokens[pos] == "-print" || grpTokens[pos] == "-print0" {
						pos++
						continue
					}
					cond, next, err := parseCond(grpTokens, pos)
					if err != nil {
						fmt.Fprintf(hc.Stderr, "%v\n", err)
						return interp.ExitStatus(1)
					}
					conds = append(conds, cond)
					pos = next
				}
				orGroups = append(orGroups, conds)
				grpTokens = []string{}
			} else {
				grpTokens = append(grpTokens, t)
			}
		}
		// Final (or only) group.
		{
			var conds andGroup
			pos := 0
			for pos < len(grpTokens) {
				if grpTokens[pos] == "-print" || grpTokens[pos] == "-print0" {
					pos++
					continue
				}
				cond, next, err := parseCond(grpTokens, pos)
				if err != nil {
					fmt.Fprintf(hc.Stderr, "%v\n", err)
					return interp.ExitStatus(1)
				}
				conds = append(conds, cond)
				pos = next
			}
			orGroups = append(orGroups, conds)
		}

		// matchEntry returns true if any OR-group fully matches (all AND conditions true).
		matchEntry := func(relPath string, d fs.DirEntry, depth int) (bool, error) {
			for _, grp := range orGroups {
				all := true
				for _, cond := range grp {
					ok, err := cond(relPath, d, depth)
					if err != nil {
						return false, err
					}
					if !ok {
						all = false
						break
					}
				}
				if all {
					return true, nil
				}
			}
			return false, nil
		}

		// Scan tokens for -maxdepth to enable early WalkDir pruning.
		// Use the minimum value if specified multiple times.
		maxDepthLimit := -1
		for ti := 0; ti+1 < len(tokens); ti++ {
			if tokens[ti] == "-maxdepth" {
				if n, err := strconv.Atoi(tokens[ti+1]); err == nil {
					if maxDepthLimit < 0 || n < maxDepthLimit {
						maxDepthLimit = n
					}
				}
			}
		}

		sep := "\n"
		if print0 {
			sep = "\x00"
		}

		for _, startPath := range startPaths {
			absStart, err := resolveInTmpDir(hc, tmpDir, startPath)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "find: %v\n", err)
				return interp.ExitStatus(1)
			}
			displayStart := filepath.Clean(startPath)

			err = filepath.WalkDir(absStart, func(absPath string, d fs.DirEntry, werr error) error {
				if werr != nil {
					fmt.Fprintf(hc.Stderr, "find: %v\n", werr)
					return nil
				}

				rel, _ := filepath.Rel(absStart, absPath)
				depth := 0
				if rel != "." {
					depth = strings.Count(rel, string(filepath.Separator)) + 1
				}

				var displayPath string
				if rel == "." {
					displayPath = displayStart
				} else {
					displayPath = filepath.Join(displayStart, rel)
				}

				ok, err := matchEntry(displayPath, d, depth)
				if err != nil {
					fmt.Fprintf(hc.Stderr, "find: %v\n", err)
					return nil
				}
				if ok {
					fmt.Fprintf(hc.Stdout, "%s%s", displayPath, sep)
				}
				// Prune descent: don't descend into directories beyond -maxdepth.
				if d.IsDir() && maxDepthLimit >= 0 && depth >= maxDepthLimit {
					return filepath.SkipDir
				}
				return nil
			})
			if err != nil {
				fmt.Fprintf(hc.Stderr, "find: %v\n", err)
				return interp.ExitStatus(1)
			}
		}

		return nil
	}
}

// parseFindSize parses a find -size argument like +10k, -2M, 100c.
// Returns comparison operator ('+', '-', or 0 for exact), size in bytes, error.
func parseFindSize(s string) (byte, int64, error) {
	if len(s) == 0 {
		return 0, 0, fmt.Errorf("empty size")
	}
	var cmp byte
	if s[0] == '+' || s[0] == '-' {
		cmp = s[0]
		s = s[1:]
	}
	if len(s) == 0 {
		return 0, 0, fmt.Errorf("empty size after sign")
	}
	mult := int64(512) // default POSIX blocks
	suffix := s[len(s)-1]
	switch suffix {
	case 'c':
		mult = 1
		s = s[:len(s)-1]
	case 'k':
		mult = 1024
		s = s[:len(s)-1]
	case 'M':
		mult = 1024 * 1024
		s = s[:len(s)-1]
	case 'G':
		mult = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid number %q", s)
	}
	return cmp, n * mult, nil
}
