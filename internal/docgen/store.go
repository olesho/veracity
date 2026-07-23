package docgen

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/olesho/veracity/internal/docgen/ir"
)

// summariesSchemaVersion is the version of the committed summaries store.
const summariesSchemaVersion = 1

// summariesRel is the store path within a project's docs directory. It is
// committed so agent-written prose persists (and is reused) until the code it
// describes changes.
const summariesRel = "docs/summaries.json"

// Summaries is the content-hash-keyed store of agent-written prose.
type Summaries struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Modules       map[string]ModuleSummary `json:"modules"`
}

// ModuleSummary holds a module's prose plus the content hash it was written for.
type ModuleSummary struct {
	SummaryHash string                      `json:"summaryHash"`
	Summary     string                      `json:"summary"`
	Interfaces  map[string]InterfaceSummary `json:"interfaces,omitempty"`
}

// InterfaceSummary holds an interface's description plus its content hash.
type InterfaceSummary struct {
	DescHash    string `json:"descHash"`
	Description string `json:"description"`
}

func newSummaries() *Summaries {
	return &Summaries{SchemaVersion: summariesSchemaVersion, Modules: map[string]ModuleSummary{}}
}

func summariesPath(projectDir string) string {
	return filepath.Join(projectDir, filepath.FromSlash(summariesRel))
}

// loadSummaries reads the store for a project; a missing store yields an empty
// one (a project simply has no summaries yet).
func loadSummaries(projectDir string) (*Summaries, error) {
	data, err := os.ReadFile(summariesPath(projectDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newSummaries(), nil
		}
		return nil, err
	}
	var s Summaries
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Modules == nil {
		s.Modules = map[string]ModuleSummary{}
	}
	return &s, nil
}

// save writes the store deterministically (map keys sorted by encoding/json).
func (s *Summaries) save(projectDir string) error {
	if err := os.MkdirAll(filepath.Dir(summariesPath(projectDir)), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(summariesPath(projectDir), append(data, '\n'), 0o644)
}

// moduleSummary returns a module's summary only if it was written for the
// module's current content hash (fresh); otherwise "" (stale/missing).
func (s *Summaries) moduleSummary(m ir.Module) string {
	ms, ok := s.Modules[m.ID]
	if !ok || ms.SummaryHash != m.ContentHash {
		return ""
	}
	return ms.Summary
}

// interfaceDescription returns a fresh interface description or "".
func (s *Summaries) interfaceDescription(m ir.Module, iface ir.Interface) string {
	ms, ok := s.Modules[m.ID]
	if !ok {
		return ""
	}
	is, ok := ms.Interfaces[iface.Name]
	if !ok || is.DescHash != iface.ContentHash {
		return ""
	}
	return is.Description
}

// setModuleSummary stores a module's prose keyed by its current content hash.
func (s *Summaries) setModuleSummary(m ir.Module, summary string) {
	ms := s.Modules[m.ID]
	ms.SummaryHash = m.ContentHash
	ms.Summary = summary
	if ms.Interfaces == nil {
		ms.Interfaces = map[string]InterfaceSummary{}
	}
	s.Modules[m.ID] = ms
}

// setInterfaceDescription stores an interface's prose keyed by its content hash.
func (s *Summaries) setInterfaceDescription(m ir.Module, iface ir.Interface, desc string) {
	ms, ok := s.Modules[m.ID]
	if !ok {
		ms = ModuleSummary{}
	}
	if ms.Interfaces == nil {
		ms.Interfaces = map[string]InterfaceSummary{}
	}
	ms.Interfaces[iface.Name] = InterfaceSummary{DescHash: iface.ContentHash, Description: desc}
	s.Modules[m.ID] = ms
}
