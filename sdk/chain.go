package sdk

import "github.com/ethndotsh/switchboard"

type Rule func(Request) Action

// Chain runs rules in order: Next/Rewrite results accumulate and later rules
// see the request as patched so far; the first deny, redirect, or respond
// short-circuits carrying the accumulated state (terminal rule wins ties).
func Chain(req Request, rules ...Rule) Action {
	current := req
	combined := Next()
	for _, rule := range rules {
		action := rule(current)
		switch action.Decision {
		case "", DecisionNext, DecisionRewrite:
			combined.Patch = mergePatch(combined.Patch, action.Patch)
			combined.Response.Headers = append(combined.Response.Headers, action.Response.Headers...)
			combined.Metadata = mergeMetadata(combined.Metadata, action.Metadata)
			if action.Reason != "" {
				combined.Reason = action.Reason
			}
			current = current.WithPatch(action.Patch)
			continue
		default:
			action.Patch = mergePatch(combined.Patch, action.Patch)
			action.Response.Headers = append(append([]switchboard.HeaderOp(nil), combined.Response.Headers...), action.Response.Headers...)
			action.Metadata = mergeMetadata(combined.Metadata, action.Metadata)
			if action.Reason == "" {
				action.Reason = combined.Reason
			}
			return action
		}
	}
	if combined.Patch.Host != nil || combined.Patch.Path != nil || combined.Patch.Query != nil {
		combined.Decision = DecisionRewrite
	}
	return combined
}

func mergePatch(base, overlay switchboard.RequestPatch) switchboard.RequestPatch {
	merged := switchboard.RequestPatch{
		Headers: append(append([]switchboard.HeaderOp(nil), base.Headers...), overlay.Headers...),
		Host:    base.Host,
		Path:    base.Path,
		Query:   base.Query,
	}
	if len(merged.Headers) == 0 {
		merged.Headers = nil
	}
	if overlay.Host != nil {
		merged.Host = overlay.Host
	}
	if overlay.Path != nil {
		merged.Path = overlay.Path
	}
	if overlay.Query != nil {
		merged.Query = overlay.Query
	}
	return merged
}

func mergeMetadata(base, overlay map[string]string) map[string]string {
	if len(overlay) == 0 {
		return base
	}
	if base == nil {
		base = make(map[string]string, len(overlay))
	}
	for key, value := range overlay {
		base[key] = value
	}
	return base
}
