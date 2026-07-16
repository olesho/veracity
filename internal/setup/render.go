package setup

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"text/template"

	"github.com/olesho/harness/assets"
	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/ownership"
)

// renderFile is one file to place in the managed project, tagged with its
// ownership kind.
type renderFile struct {
	Rel     string
	Content []byte
	Kind    ownership.Kind
}

type tmplData struct {
	Name       string
	ModulePath string
	RepoName   string
}

func renderAsset(assetPath string, data tmplData) ([]byte, error) {
	raw, err := assets.Read(assetPath)
	if err != nil {
		return nil, fmt.Errorf("reading asset %s: %w", assetPath, err)
	}
	if !strings.HasSuffix(assetPath, ".tmpl") {
		return raw, nil
	}
	t, err := template.New(path.Base(assetPath)).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parsing template %s: %w", assetPath, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template %s: %w", assetPath, err)
	}
	return buf.Bytes(), nil
}

// Render produces every file to write for a lock. includeSamples controls
// whether the language sample module (greeter.*) is scaffolded — true for a new
// project (setup/add), false when adopting an existing project or reconciling on
// edit (never inject sample source into real code). repoName labels agent docs.
func Render(repoName string, lock *lockfile.Lock, includeSamples bool) ([]renderFile, error) {
	var out []renderFile
	for _, p := range lock.Projects {
		pf, err := projectFiles(lock, p, includeSamples)
		if err != nil {
			return nil, err
		}
		out = append(out, pf...)
	}
	wf, err := wiringFiles(repoName, lock)
	if err != nil {
		return nil, err
	}
	out = append(out, wf...)
	return out, nil
}

type fileSpec struct {
	asset string
	dest  string
}

// projectConfigSpecs are the native config / manifest files (seeded once, then
// owned by the user; harmlessly skipped when they already exist).
func projectConfigSpecs(lang string) []fileSpec {
	switch lang {
	case lockfile.LangGo:
		return []fileSpec{
			{"templates/go/go.mod.tmpl", "go.mod"},
			{"templates/go/golangci.yml", ".golangci.yml"},
		}
	case lockfile.LangPython:
		return []fileSpec{{"templates/python/pyproject.toml.tmpl", "pyproject.toml"}}
	case lockfile.LangTS:
		return []fileSpec{
			{"templates/typescript/package.json.tmpl", "package.json"},
			{"templates/typescript/tsconfig.json", "tsconfig.json"},
			{"templates/typescript/eslint.config.js", "eslint.config.js"},
			{"templates/typescript/prettierrc.json", ".prettierrc.json"},
			{"templates/typescript/vitest.config.ts", "vitest.config.ts"},
		}
	default:
		return nil
	}
}

// projectSampleSpecs are the sample module source files (a real project keeps
// its own code; these are only for a freshly scaffolded project).
func projectSampleSpecs(lang string) []fileSpec {
	switch lang {
	case lockfile.LangGo:
		return []fileSpec{
			{"templates/go/greeter.go.tmpl", "example/greeter.go"},
			{"templates/go/greeter_test.go.tmpl", "example/greeter_test.go"},
		}
	case lockfile.LangPython:
		return []fileSpec{
			{"templates/python/example/__init__.py", "example/__init__.py"},
			{"templates/python/example/greeter.py", "example/greeter.py"},
			{"templates/python/tests/test_greeter.py", "tests/test_greeter.py"},
		}
	case lockfile.LangTS:
		return []fileSpec{
			{"templates/typescript/src/greeter.ts", "src/greeter.ts"},
			{"templates/typescript/test/greeter.test.ts", "test/greeter.test.ts"},
		}
	default:
		return nil
	}
}

func projectFiles(lock *lockfile.Lock, p lockfile.Project, includeSamples bool) ([]renderFile, error) {
	base := p.Path(lock.Layout)
	join := func(rel string) string {
		if base == "." {
			return rel
		}
		return base + "/" + rel
	}
	data := tmplData{Name: p.Name, ModulePath: p.ModulePath}

	specs := projectConfigSpecs(p.Language)
	if includeSamples {
		specs = append(specs, projectSampleSpecs(p.Language)...)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("unsupported language %q", p.Language)
	}

	out := make([]renderFile, 0, len(specs))
	for _, s := range specs {
		content, err := renderAsset(s.asset, data)
		if err != nil {
			return nil, err
		}
		// Project source and native config are seeded once, then owned by the user.
		out = append(out, renderFile{Rel: join(s.dest), Content: content, Kind: ownership.Owned})
	}
	return out, nil
}

func anyDocsFeature(lock *lockfile.Lock) bool {
	for _, p := range lock.Projects {
		if p.Features.Markdown || p.Features.Diagrams {
			return true
		}
	}
	return false
}

func wiringFiles(repoName string, lock *lockfile.Lock) ([]renderFile, error) {
	caps := lock.Capabilities
	data := tmplData{RepoName: repoName}
	var out []renderFile
	add := func(asset, dest string, kind ownership.Kind) error {
		content, err := renderAsset(asset, data)
		if err != nil {
			return err
		}
		out = append(out, renderFile{Rel: dest, Content: content, Kind: kind})
		return nil
	}

	hasAgent := func(a string) bool {
		for _, x := range caps.Agents {
			if x == a {
				return true
			}
		}
		return false
	}

	if len(caps.Agents) > 0 {
		for _, sh := range []string{"post-edit.sh", "stop.sh", "session-start.sh"} {
			if err := add("wiring/hooks/"+sh, "hooks/"+sh, ownership.Managed); err != nil {
				return nil, err
			}
		}
	}
	if hasAgent(lockfile.AgentClaude) {
		if err := add("wiring/claude-settings.json", ".claude/settings.json", ownership.Managed); err != nil {
			return nil, err
		}
		if caps.Skills {
			if err := add("wiring/claude-project-skill.md", ".claude/skills/harness/SKILL.md", ownership.Managed); err != nil {
				return nil, err
			}
			// The docs skill is only useful when a project opts into docs.
			if anyDocsFeature(lock) {
				if err := add("wiring/claude-docs-skill.md", ".claude/skills/harness-docs/SKILL.md", ownership.Managed); err != nil {
					return nil, err
				}
			}
		}
	}
	if hasAgent(lockfile.AgentCodex) {
		if err := add("wiring/codex-config.toml", ".codex/config.toml", ownership.Managed); err != nil {
			return nil, err
		}
	}
	if caps.CI {
		if err := add("wiring/ci.yml", ".github/workflows/ci.yml", ownership.Managed); err != nil {
			return nil, err
		}
		if err := add("wiring/ci-setup.md", "docs/ci-setup.md", ownership.Managed); err != nil {
			return nil, err
		}
	}
	if caps.AgentDocs {
		if err := add("wiring/CLAUDE.md.tmpl", "CLAUDE.md", ownership.Managed); err != nil {
			return nil, err
		}
		if err := add("wiring/AGENTS.md.tmpl", "AGENTS.md", ownership.Managed); err != nil {
			return nil, err
		}
		if err := add("wiring/SETUP.md", "SETUP.md", ownership.Managed); err != nil {
			return nil, err
		}
	}
	// Always seed a .gitignore (owned; user may extend it).
	if err := add("wiring/gitignore", ".gitignore", ownership.Owned); err != nil {
		return nil, err
	}
	return out, nil
}
