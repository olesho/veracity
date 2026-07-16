package docgen

import (
	"encoding/json"
	"fmt"

	"github.com/olesho/harness/internal/docgen/ir"
	"github.com/olesho/harness/internal/lockfile"
)

// EnrichInput is the JSON the agent submits: module summaries and per-interface
// descriptions. It is validated against the current IR so hallucinated modules
// or interfaces are rejected rather than stored.
type EnrichInput struct {
	Modules map[string]EnrichModule `json:"modules"`
}

// EnrichModule is one module's prose in an EnrichInput.
type EnrichModule struct {
	Summary    string            `json:"summary,omitempty"`
	Interfaces map[string]string `json:"interfaces,omitempty"`
}

// EnrichResult reports how many items were stored.
type EnrichResult struct {
	Modules    int
	Interfaces int
}

// Enrich ingests agent-written prose for a project, validates it against the
// current IR, stores each item keyed by the current content hash, and saves the
// store. Unknown module IDs or interface names are rejected.
func Enrich(root string, lock *lockfile.Lock, proj lockfile.Project, data []byte) (EnrichResult, error) {
	var in EnrichInput
	if err := json.Unmarshal(data, &in); err != nil {
		return EnrichResult{}, fmt.Errorf("parsing enrichment: %w", err)
	}
	doc, err := ExtractProject(root, lock, proj)
	if err != nil {
		return EnrichResult{}, err
	}
	byID := map[string]ir.Module{}
	for _, m := range doc.Modules {
		byID[m.ID] = m
	}

	store, err := loadSummaries(projectDir(root, lock, proj))
	if err != nil {
		return EnrichResult{}, err
	}

	var res EnrichResult
	for id, em := range in.Modules {
		mod, ok := byID[id]
		if !ok {
			return EnrichResult{}, fmt.Errorf("unknown module %q (not in the current IR)", id)
		}
		if em.Summary != "" {
			store.setModuleSummary(mod, em.Summary)
			res.Modules++
		}
		ifaceByName := map[string]ir.Interface{}
		for _, iface := range mod.Interfaces {
			ifaceByName[iface.Name] = iface
		}
		for name, desc := range em.Interfaces {
			iface, ok := ifaceByName[name]
			if !ok {
				return EnrichResult{}, fmt.Errorf("unknown interface %q in module %q", name, id)
			}
			store.setInterfaceDescription(mod, iface, desc)
			res.Interfaces++
		}
	}

	if err := store.save(projectDir(root, lock, proj)); err != nil {
		return EnrichResult{}, err
	}
	return res, nil
}
