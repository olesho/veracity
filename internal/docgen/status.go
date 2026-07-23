package docgen

import (
	"bytes"
	"encoding/json"

	"github.com/olesho/veracity/internal/docgen/ir"
	"github.com/olesho/veracity/internal/lockfile"
)

// StatusReport is the machine-readable structure list the Claude Code agent
// reads to decide what prose to write. It combines the deterministic IR with the
// freshness of the stored summaries.
type StatusReport struct {
	Subproject string         `json:"subproject"`
	Language   string         `json:"language"`
	Modules    []ModuleStatus `json:"modules"`
}

// ModuleStatus is a module plus its summary freshness.
type ModuleStatus struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Path         string            `json:"path"`
	DocComment   string            `json:"docComment,omitempty"`
	Summary      string            `json:"summary,omitempty"`
	NeedsSummary bool              `json:"needsSummary"`
	Exports      []ir.Export       `json:"exports"`
	Interfaces   []InterfaceStatus `json:"interfaces"`
}

// InterfaceStatus is an interface plus its description freshness.
type InterfaceStatus struct {
	Name             string      `json:"name"`
	DocComment       string      `json:"docComment,omitempty"`
	Methods          []ir.Method `json:"methods"`
	Description      string      `json:"description,omitempty"`
	NeedsDescription bool        `json:"needsDescription"`
}

// Status extracts a project's IR and reports which module/interface summaries
// are fresh, stale, or missing.
func Status(root string, lock *lockfile.Lock, proj lockfile.Project) (StatusReport, error) {
	doc, err := ExtractProject(root, lock, proj)
	if err != nil {
		return StatusReport{}, err
	}
	store, err := loadSummaries(projectDir(root, lock, proj))
	if err != nil {
		return StatusReport{}, err
	}

	rep := StatusReport{Subproject: doc.Subproject, Language: doc.Language}
	for _, m := range doc.Modules {
		summary := store.moduleSummary(m)
		ms := ModuleStatus{
			ID:           m.ID,
			Name:         m.Name,
			Path:         m.Path,
			DocComment:   m.DocComment,
			Summary:      summary,
			NeedsSummary: summary == "",
			Exports:      m.Exports,
		}
		for _, iface := range m.Interfaces {
			desc := store.interfaceDescription(m, iface)
			ms.Interfaces = append(ms.Interfaces, InterfaceStatus{
				Name:             iface.Name,
				DocComment:       iface.DocComment,
				Methods:          iface.Methods,
				Description:      desc,
				NeedsDescription: desc == "",
			})
		}
		rep.Modules = append(rep.Modules, ms)
	}
	return rep, nil
}

// JSON renders a StatusReport as indented JSON.
func (r StatusReport) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EnrichTemplate returns a ready-to-fill `veracity docs enrich` payload
// containing exactly the modules/interfaces whose prose is pending, with empty
// strings for the agent to fill in. This removes any guesswork about the shape.
func (r StatusReport) EnrichTemplate() ([]byte, error) {
	type modTmpl struct {
		Summary    string            `json:"summary"`
		Interfaces map[string]string `json:"interfaces,omitempty"`
	}
	mods := map[string]modTmpl{}
	for _, m := range r.Modules {
		if !m.NeedsSummary {
			pendingIfaces := map[string]string{}
			for _, i := range m.Interfaces {
				if i.NeedsDescription {
					pendingIfaces[i.Name] = ""
				}
			}
			if len(pendingIfaces) == 0 {
				continue
			}
			mods[m.ID] = modTmpl{Interfaces: pendingIfaces}
			continue
		}
		mt := modTmpl{}
		for _, i := range m.Interfaces {
			if i.NeedsDescription {
				if mt.Interfaces == nil {
					mt.Interfaces = map[string]string{}
				}
				mt.Interfaces[i.Name] = ""
			}
		}
		mods[m.ID] = mt
	}
	out := map[string]any{"modules": mods}
	return json.MarshalIndent(out, "", "  ")
}

// PendingCount returns the number of module summaries + interface descriptions
// that are stale or missing.
func (r StatusReport) PendingCount() int {
	n := 0
	for _, m := range r.Modules {
		if m.NeedsSummary {
			n++
		}
		for _, i := range m.Interfaces {
			if i.NeedsDescription {
				n++
			}
		}
	}
	return n
}
