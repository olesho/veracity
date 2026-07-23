package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/olesho/veracity/assets"
	"github.com/olesho/veracity/internal/docgen"
	"github.com/olesho/veracity/internal/lockfile"
	"github.com/olesho/veracity/internal/setup"
	"github.com/olesho/veracity/internal/toolchain"
)

func lookPath(name string) (string, error) { return exec.LookPath(name) }

func readConfig(pathOrDash string, stdin io.Reader) ([]byte, error) {
	if pathOrDash == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(pathOrDash)
}

func onOff(v string) *bool {
	switch v {
	case "on", "true", "yes":
		b := true
		return &b
	case "off", "false", "no":
		b := false
		return &b
	default:
		return nil
	}
}

func reportConflicts(res *setup.Result, errw io.Writer) {
	for _, c := range res.Conflicts {
		fmt.Fprintf(errw, "CONFLICT: %s was modified locally; wrote %s.veracity-new instead (reconcile manually)\n", c, c)
	}
}

func cmdList(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(errw)
	asJSON := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	lock, err := lockfile.Load(resolveRoot())
	if err != nil {
		fmt.Fprintf(errw, "veracity list: %v\n", err)
		return 1
	}
	if *asJSON {
		type row struct {
			Name     string            `json:"name"`
			Language string            `json:"language"`
			Path     string            `json:"path"`
			Features lockfile.Features `json:"features"`
		}
		rows := make([]row, 0, len(lock.Projects))
		for _, p := range lock.Projects {
			rows = append(rows, row{p.Name, p.Language, p.Path(lock.Layout), p.Features})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
		return 0
	}
	for _, p := range lock.Projects {
		fmt.Fprintf(out, "%-16s %-11s %s\n", p.Name, p.Language, p.Path(lock.Layout))
	}
	return 0
}

func cmdLockQuery(args []string, out, errw io.Writer) int {
	if len(args) < 1 || isFlag(args[0]) {
		fmt.Fprintln(errw, "usage: veracity lock-query <name> [--json]")
		return 2
	}
	name := args[0]
	fs := flag.NewFlagSet("lock-query", flag.ContinueOnError)
	fs.SetOutput(errw)
	_ = fs.Bool("json", true, "output JSON (always on; accepted for consistency)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	lock, err := lockfile.Load(resolveRoot())
	if err != nil {
		fmt.Fprintf(errw, "veracity lock-query: %v\n", err)
		return 1
	}
	p, ok := lock.Find(name)
	if !ok {
		fmt.Fprintf(errw, "veracity lock-query: no such project %q\n", fs.Arg(0))
		return 1
	}
	type out2 struct {
		Name     string            `json:"name"`
		Language string            `json:"language"`
		Path     string            `json:"path"`
		Features lockfile.Features `json:"features"`
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out2{p.Name, p.Language, p.Path(lock.Layout), p.Features})
	return 0
}

func cmdToolchain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("toolchain", flag.ContinueOnError)
	fs.SetOutput(errw)
	lang := fs.String("language", "", "language: go|python|typescript")
	_ = fs.Bool("json", true, "output JSON (always on; accepted for consistency)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *lang == "" {
		fmt.Fprintln(errw, "usage: veracity toolchain --language <go|python|typescript> [--json]")
		return 2
	}
	data, err := toolchain.JSON(*lang)
	if err != nil {
		fmt.Fprintf(errw, "veracity toolchain: %v\n", err)
		return 1
	}
	_, _ = out.Write(data)
	return 0
}

func cmdDocs(args []string, stdin io.Reader, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: veracity docs <markdown|render|status|enrich> [flags]")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "markdown":
		return docsRegen(rest, out, errw, false)
	case "render":
		return docsRegen(rest, out, errw, true)
	case "status":
		return docsStatus(rest, out, errw)
	case "enrich":
		return docsEnrich(rest, stdin, out, errw)
	default:
		fmt.Fprintf(errw, "veracity docs: unknown subcommand %q (want markdown|render|status|enrich)\n", sub)
		return 2
	}
}

// docsRegen runs markdown-only (render=false) or all outputs (render=true).
func docsRegen(args []string, out, errw io.Writer, render bool) int {
	fs := flag.NewFlagSet("docs", flag.ContinueOnError)
	fs.SetOutput(errw)
	changed := fs.Bool("changed", false, "only regenerate for projects changed in the working tree")
	_ = fs.Bool("all", false, "regenerate for all enabled projects (default)")
	force := fs.Bool("force", false, "write even when output is unchanged")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := resolveRoot()
	lock, err := lockfile.Load(root)
	if err != nil {
		fmt.Fprintf(errw, "veracity docs: %v\n", err)
		return 1
	}
	fn := docgen.Markdown
	if render {
		fn = docgen.Render
	}
	if _, err := fn(root, lock, *changed, *force, out); err != nil {
		fmt.Fprintf(errw, "veracity docs: %v\n", err)
		return 1
	}
	return 0
}

func docsStatus(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("docs status", flag.ContinueOnError)
	fs.SetOutput(errw)
	asJSON := fs.Bool("json", false, "output JSON (the structures list for the agent)")
	tmpl := fs.Bool("template", false, "output a ready-to-fill `docs enrich` payload for the pending items")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := resolveRoot()
	lock, err := lockfile.Load(root)
	if err != nil {
		fmt.Fprintf(errw, "veracity docs status: %v\n", err)
		return 1
	}
	proj, ok := resolveDocProject(lock, fs.Args())
	if !ok {
		fmt.Fprintln(errw, "veracity docs status: specify a project name (monorepo) or none (single)")
		return 2
	}
	rep, err := docgen.Status(root, lock, proj)
	if err != nil {
		fmt.Fprintf(errw, "veracity docs status: %v\n", err)
		return 1
	}
	if *tmpl {
		data, err := rep.EnrichTemplate()
		if err != nil {
			fmt.Fprintf(errw, "veracity docs status: %v\n", err)
			return 1
		}
		_, _ = out.Write(append(data, '\n'))
		return 0
	}
	if *asJSON {
		data, err := rep.JSON()
		if err != nil {
			fmt.Fprintf(errw, "veracity docs status: %v\n", err)
			return 1
		}
		_, _ = out.Write(data)
		return 0
	}
	fmt.Fprintf(out, "%s: %d module(s), %d summary/description item(s) pending\n", rep.Subproject, len(rep.Modules), rep.PendingCount())
	for _, m := range rep.Modules {
		status := "fresh"
		if m.NeedsSummary {
			status = "PENDING"
		}
		fmt.Fprintf(out, "  module %-24s %s\n", m.Name, status)
	}
	return 0
}

func docsEnrich(args []string, stdin io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("docs enrich", flag.ContinueOnError)
	fs.SetOutput(errw)
	from := fs.String("from", "-", "path to a summaries JSON, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := resolveRoot()
	lock, err := lockfile.Load(root)
	if err != nil {
		fmt.Fprintf(errw, "veracity docs enrich: %v\n", err)
		return 1
	}
	proj, ok := resolveDocProject(lock, fs.Args())
	if !ok {
		fmt.Fprintln(errw, "veracity docs enrich: specify a project name (monorepo) or none (single)")
		return 2
	}
	data, err := readConfig(*from, stdin)
	if err != nil {
		fmt.Fprintf(errw, "veracity docs enrich: %v\n", err)
		return 1
	}
	res, err := docgen.Enrich(root, lock, proj, data)
	if err != nil {
		fmt.Fprintf(errw, "veracity docs enrich: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "enriched %d module summary(ies), %d interface description(s)\n", res.Modules, res.Interfaces)
	return 0
}

// resolveDocProject picks the target project: the named one, or the sole project
// in a single-layout repo.
func resolveDocProject(lock *lockfile.Lock, args []string) (lockfile.Project, bool) {
	if len(args) > 0 {
		return lock.Find(args[0])
	}
	if len(lock.Projects) == 1 {
		return lock.Projects[0], true
	}
	return lockfile.Project{}, false
}

func cmdInstallSkills(out, errw io.Writer) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errw, "veracity install-skills: %v\n", err)
		return 1
	}
	installs := []struct{ asset, dest string }{
		{"global-frontend/claude/veracity-setup/SKILL.md", filepath.Join(home, ".claude", "skills", "veracity-setup", "SKILL.md")},
		{"global-frontend/codex/veracity-setup.md", filepath.Join(home, ".codex", "prompts", "veracity-setup.md")},
	}
	for _, in := range installs {
		data, err := assets.Read(in.asset)
		if err != nil {
			fmt.Fprintf(errw, "veracity install-skills: %v\n", err)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(in.dest), 0o755); err != nil {
			fmt.Fprintf(errw, "veracity install-skills: %v\n", err)
			return 1
		}
		if err := os.WriteFile(in.dest, data, 0o644); err != nil {
			fmt.Fprintf(errw, "veracity install-skills: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "installed %s\n", in.dest)
	}
	return 0
}
