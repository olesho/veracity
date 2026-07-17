package setup

import (
	"encoding/json"
	"fmt"

	"github.com/olesho/harness/internal/lockfile"
)

// Input is the setup configuration accepted on stdin (`harness setup --config -`)
// or composed from a preset. Optional fields are pointers so a preset default
// can fill an unspecified value while an explicit false still overrides.
type Input struct {
	Layout       string         `json:"layout"`
	Preset       string         `json:"preset,omitempty"`
	Capabilities *CapsInput     `json:"capabilities,omitempty"`
	Projects     []ProjectInput `json:"projects"`
}

// CapsInput mirrors lockfile.Capabilities with optional (pointer) fields.
type CapsInput struct {
	Agents    *[]string `json:"agents,omitempty"`
	GitHooks  *bool     `json:"gitHooks,omitempty"`
	CI        *bool     `json:"ci,omitempty"`
	AgentDocs *bool     `json:"agentDocs,omitempty"`
	Skills    *bool     `json:"skills,omitempty"`
}

// ProjectInput describes one project to scaffold.
type ProjectInput struct {
	Name        string         `json:"name"`
	Language    string         `json:"language"`
	ModulePath  string         `json:"modulePath,omitempty"`
	Features    *FeaturesInput `json:"features,omitempty"`
	CoverageMin *int           `json:"coverageMin,omitempty"` // Go only; nil = preset default
}

// FeaturesInput mirrors lockfile.Features with optional (pointer) fields.
type FeaturesInput struct {
	Lint     *bool `json:"lint,omitempty"`
	Test     *bool `json:"test,omitempty"`
	Markdown *bool `json:"markdown,omitempty"`
	Diagrams *bool `json:"diagrams,omitempty"`
	// Go-only quality verifiers.
	Gofumpt  *bool `json:"gofumpt,omitempty"`
	Gci      *bool `json:"gci,omitempty"`
	ModTidy  *bool `json:"modTidy,omitempty"`
	Coverage *bool `json:"coverage,omitempty"`
}

// Presets.
const (
	PresetMinimal  = "minimal"
	PresetStandard = "standard"
	PresetFull     = "full"
)

type presetDefaults struct {
	name     string
	caps     lockfile.Capabilities
	features lockfile.Features
}

// goFeatureDefaults returns the Go-only verifier defaults for this preset. They
// are applied only to Go projects (see resolveProject); other languages never
// receive them.
func (d presetDefaults) goFeatureDefaults() lockfile.Features {
	if d.name == PresetFull {
		return lockfile.Features{Gofumpt: true, Gci: true, ModTidy: true, Coverage: true}
	}
	return lockfile.Features{}
}

func presetFor(name string) (presetDefaults, error) {
	switch name {
	case "", PresetStandard:
		return presetDefaults{
			name:     PresetStandard,
			caps:     lockfile.Capabilities{Agents: []string{}, GitHooks: true},
			features: lockfile.Features{Lint: true, Test: true},
		}, nil
	case PresetMinimal:
		return presetDefaults{
			name:     PresetMinimal,
			caps:     lockfile.Capabilities{Agents: []string{}},
			features: lockfile.Features{Lint: true},
		}, nil
	case PresetFull:
		return presetDefaults{
			name:     PresetFull,
			caps:     lockfile.Capabilities{Agents: []string{lockfile.AgentClaude, lockfile.AgentCodex}, GitHooks: true, CI: true, AgentDocs: true, Skills: true},
			features: lockfile.Features{Lint: true, Test: true, Markdown: true},
		}, nil
	default:
		return presetDefaults{}, fmt.Errorf("unknown preset %q (want minimal|standard|full)", name)
	}
}

// ParseInput decodes setup Input JSON.
func ParseInput(data []byte) (*Input, error) {
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("parsing setup config: %w", err)
	}
	return &in, nil
}

// Resolve applies preset defaults and invariants to Input and returns a
// validated Lock ready to render and persist.
func Resolve(in *Input) (*lockfile.Lock, error) {
	def, err := presetFor(in.Preset)
	if err != nil {
		return nil, err
	}

	layout := in.Layout
	if layout == "" {
		layout = lockfile.LayoutSingle
	}
	if layout != lockfile.LayoutSingle && layout != lockfile.LayoutMonorepo {
		return nil, fmt.Errorf("invalid layout %q (want single|monorepo)", layout)
	}
	if len(in.Projects) == 0 {
		return nil, fmt.Errorf("at least one project is required")
	}
	if layout == lockfile.LayoutSingle && len(in.Projects) != 1 {
		return nil, fmt.Errorf("single layout requires exactly 1 project, got %d", len(in.Projects))
	}

	caps := def.caps
	if c := in.Capabilities; c != nil {
		if c.Agents != nil {
			caps.Agents = append([]string{}, (*c.Agents)...)
		}
		if c.GitHooks != nil {
			caps.GitHooks = *c.GitHooks
		}
		if c.CI != nil {
			caps.CI = *c.CI
		}
		if c.AgentDocs != nil {
			caps.AgentDocs = *c.AgentDocs
		}
		if c.Skills != nil {
			caps.Skills = *c.Skills
		}
	}
	if caps.Agents == nil {
		caps.Agents = []string{}
	}

	var projects []lockfile.Project
	for _, p := range in.Projects {
		proj, err := resolveProject(p, def)
		if err != nil {
			return nil, err
		}
		projects = append(projects, proj)
	}

	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion,
		Layout:        layout,
		Capabilities:  caps,
		Projects:      projects,
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	return lock, nil
}
