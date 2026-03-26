package docs_test

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/justyn-clark/wakeplane/internal/cli"
	"github.com/justyn-clark/wakeplane/internal/docsgen"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

type codeFence struct {
	Info    string
	Content string
}

func TestGeneratedPublicDocsAreCurrent(t *testing.T) {
	repoRoot := repoRoot(t)
	if err := docsgen.Check(repoRoot); err != nil {
		t.Fatal(err)
	}
}

func TestPublicMarkdownCommandsMatchSurface(t *testing.T) {
	root := cli.NewRootCmd("test")
	routeSet := map[string]struct{}{}
	routes, err := docsgen.Routes(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		routeSet[route.Path] = struct{}{}
	}

	for _, path := range publicMarkdownFiles(t) {
		for _, fence := range markdownFences(t, path) {
			switch fence.Info {
			case "bash", "sh", "shell", "text":
				for _, line := range shellCommands(fence.Content) {
					switch {
					case strings.Contains(line, "wakeplane"), strings.Contains(line, "wakeplaned"):
						validateWakeplaneCommand(t, root, path, line)
					case strings.HasPrefix(line, "curl "):
						validateCurlLine(t, routeSet, path, line)
					}
				}
			}
		}
	}
}

func TestPublicStructuredSnippetsDecode(t *testing.T) {
	for _, path := range publicMarkdownFiles(t) {
		for _, fence := range markdownFences(t, path) {
			switch fence.Info {
			case "yaml":
				validateYAMLBlock(t, path, fence.Content)
			case "json":
				validateJSONBlock(t, path, fence.Content)
			case "go":
				validateGoBlock(t, path, fence.Content)
			}
		}
	}
}

func validateWakeplaneCommand(t *testing.T, root *cobra.Command, path, line string) {
	t.Helper()
	tokens := strings.Fields(stripShellComment(line))
	commandIndex := -1
	for i, token := range tokens {
		if token == "wakeplane" || token == "wakeplaned" {
			commandIndex = i
			break
		}
	}
	if commandIndex == -1 {
		return
	}
	current := root
	argsMode := false
	for _, token := range tokens[commandIndex+1:] {
		if token == "\\" {
			continue
		}
		if strings.HasPrefix(token, "-") {
			flagName := token
			if idx := strings.Index(flagName, "="); idx >= 0 {
				flagName = flagName[:idx]
			}
			if !flagExists(current, flagName) {
				t.Fatalf("%s: unknown flag %q in %q", path, flagName, line)
			}
			continue
		}
		if isPlaceholder(token) || argsMode {
			argsMode = true
			continue
		}
		next, _, err := current.Find([]string{token})
		if err != nil || next == current {
			argsMode = true
			continue
		}
		current = next
	}
}

func validateCurlLine(t *testing.T, routeSet map[string]struct{}, path, line string) {
	t.Helper()
	re := regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1)(?::\d+)?([^\s#]+)`)
	match := re.FindStringSubmatch(line)
	if len(match) != 2 {
		return
	}
	routePath := match[1]
	if idx := strings.Index(routePath, "?"); idx >= 0 {
		routePath = routePath[:idx]
	}
	if _, ok := routeSet[routePath]; !ok {
		t.Fatalf("%s: unknown API path %q in %q", path, routePath, line)
	}
}

func validateYAMLBlock(t *testing.T, path, block string) {
	t.Helper()
	switch {
	case strings.HasPrefix(strings.TrimSpace(block), "name:"):
		var req domain.CreateScheduleRequest
		decodeKnownYAML(t, path, block, &req)
	case hasTopLevelKey(block, "schedule"):
		var wrapper struct {
			Schedule domain.ScheduleSpec `yaml:"schedule"`
		}
		decodeKnownYAML(t, path, block, &wrapper)
	case hasTopLevelKey(block, "target"):
		var wrapper struct {
			Target domain.TargetSpec `yaml:"target"`
		}
		decodeKnownYAML(t, path, block, &wrapper)
	case hasTopLevelKey(block, "policy"):
		var wrapper struct {
			Policy domain.Policy `yaml:"policy"`
		}
		decodeKnownYAML(t, path, block, &wrapper)
	case hasTopLevelKey(block, "retry"):
		var wrapper struct {
			Retry domain.RetryPolicy `yaml:"retry"`
		}
		decodeKnownYAML(t, path, block, &wrapper)
	default:
		var generic map[string]any
		decodeKnownYAML(t, path, block, &generic)
	}
}

func validateJSONBlock(t *testing.T, path, block string) {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(block), &value); err != nil {
		t.Fatalf("%s: invalid JSON block: %v", path, err)
	}
}

func validateGoBlock(t *testing.T, path, block string) {
	t.Helper()
	candidates := []string{
		"package snippet\n\n" + block,
		"package snippet\n\nfunc _() {\n" + indent(block) + "\n}\n",
	}
	for _, candidate := range candidates {
		if _, err := parser.ParseFile(token.NewFileSet(), path, candidate, parser.AllErrors); err == nil {
			return
		}
	}
	t.Fatalf("%s: go snippet does not parse", path)
}

func decodeKnownYAML(t *testing.T, path, block string, out any) {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(block))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		t.Fatalf("%s: invalid YAML block: %v", path, err)
	}
}

func publicMarkdownFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	if err := filepath.Walk(filepath.Join(repoRoot(t), "docs/public"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func markdownFences(t *testing.T, path string) []codeFence {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	var fences []codeFence
	var current *codeFence
	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if current == nil {
				current = &codeFence{Info: strings.TrimSpace(strings.TrimPrefix(line, "```"))}
				continue
			}
			fences = append(fences, *current)
			current = nil
			continue
		}
		if current != nil {
			current.Content += line + "\n"
		}
	}
	return fences
}

func shellCommands(block string) []string {
	lines := strings.Split(block, "\n")
	var commands []string
	var current strings.Builder
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(strings.TrimSuffix(line, "\\"))
		if strings.HasSuffix(line, "\\") {
			continue
		}
		commands = append(commands, strings.TrimSpace(current.String()))
		current.Reset()
	}
	if current.Len() > 0 {
		commands = append(commands, strings.TrimSpace(current.String()))
	}
	return commands
}

func stripShellComment(line string) string {
	if idx := strings.Index(line, " #"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func hasTopLevelKey(block, key string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return strings.HasPrefix(line, key+":")
	}
	return false
}

func isPlaceholder(token string) bool {
	return strings.HasPrefix(token, "<") && strings.HasSuffix(token, ">")
}

func flagExists(cmd *cobra.Command, token string) bool {
	name := strings.TrimLeft(token, "-")
	if strings.HasPrefix(token, "--") {
		return cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil
	}
	return cmd.Flags().ShorthandLookup(name) != nil || cmd.InheritedFlags().ShorthandLookup(name) != nil
}

func indent(src string) string {
	var buf bytes.Buffer
	for _, line := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		buf.WriteString("\t")
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return strings.TrimRight(buf.String(), "\n")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(dir)
}
