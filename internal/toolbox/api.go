package toolbox

import (
	"context"
	"encoding/json"
)

// Effects declares what running a tool can do outside Facet, for every
// request the tool accepts. Adapters such as the MCP server map it onto their
// own vocabulary. Facet only declares effects: it never asks for consent or
// enforces approval, which is the calling runtime's policy.
type Effects struct {
	// MayCharge: a run may bill the operator's account at a provider. The
	// amount is not knowable in advance, so it is never inferred from cost.
	MayCharge bool `json:"may_charge"`
	// Network: a run may contact a remote service or fetch remote content,
	// including content a rendered page references.
	Network bool `json:"network"`
	// ExternalWrite: a run may create, replace or delete files outside the
	// Facet process — outputs, evidence frames, downloads, staging files.
	// Exactly the complement of ReadOnly.
	ExternalWrite bool `json:"external_write"`
	// Deterministic: the same request against the same input files produces
	// byte-identical output. Claimed only where it was measured; a networked
	// or chargeable tool is never deterministic.
	Deterministic bool `json:"deterministic"`
	// ReadOnly: a run never creates, modifies or deletes any file and never
	// changes remote state; it only reads its inputs and reports facts. It is
	// a property of the tool, so it holds for every operation the tool
	// accepts: visual_qa's probe writes nothing but its review writes frames,
	// so visual_qa is not read-only. Reading inputs, probing them and
	// querying a remote catalogue do not break it; writing anything does.
	ReadOnly bool `json:"read_only"`
}

// EffectsFor returns the declared effects of a tool. It reads the same
// sources as `facet tools list` and `describe`, so the surfaces cannot
// disagree.
//
// A name that is not a Facet tool gets the most conservative declaration —
// may charge, networked, writing, not deterministic — so a caller that names
// a tool wrongly over-gates rather than under-gates.
func EffectsFor(tool string) Effects {
	tool = canonicalToolName(tool)
	if !known(tool) {
		return Effects{MayCharge: true, Network: true, ExternalWrite: true}
	}
	return Effects{
		MayCharge:     MayCharge(tool),
		Network:       executionFor(tool).Network,
		ExternalWrite: externalWriteFor(tool, "run"),
		Deterministic: Deterministic(tool),
		ReadOnly:      readOnlyTools[tool],
	}
}

// PaidTools returns the tools whose effects may charge, sorted. Consent
// settings written for a CLI (facet wire's ask rules, a plugin's approval
// modes) are derived from it rather than from a list kept elsewhere.
func PaidTools() []string {
	var out []string
	for _, name := range Names() {
		if EffectsFor(name).MayCharge {
			out = append(out, name)
		}
	}
	return out
}

// Describe returns exactly what `facet tools describe <tool>` reports, as
// decoded JSON: capability, dependencies with their resolution, configured,
// provider, cost, effects, request_schema and result_schema. The value is a
// fresh copy the caller may modify. ok is false for an unknown tool.
func Describe(tool string) (map[string]any, bool) {
	tool = canonicalToolName(tool)
	if !known(tool) {
		return nil, false
	}
	data, err := json.Marshal(description(tool))
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

// EstimateContext validates a request through the tool's estimate path and
// reports estimated cost and effects, like `facet tools estimate`. It writes
// no output and bills nothing; cancelling ctx abandons it.
func EstimateContext(ctx context.Context, tool string, data []byte) Envelope {
	tool = canonicalToolName(tool)
	if err := ctx.Err(); err != nil {
		return errorEnvelope(tool, "estimate", failure("cancelled", err.Error(), nil))
	}
	if !known(tool) {
		return errorEnvelope(tool, "estimate", failure("unknown_tool", "unknown tool: "+tool, nil))
	}
	return estimateEnvelope(ctx, tool, data)
}
