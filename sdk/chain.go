package sdk

import "github.com/ethndotsh/switchboard"

type Rule func(Request) Action

func Chain(req Request, rules ...Rule) Action {
	current := req
	combined := Next()
	for _, rule := range rules {
		action := rule(current)
		switch action.Type {
		case "", ActionNext:
			combined.HeaderOps = append(combined.HeaderOps, action.HeaderOps...)
			current = current.WithHeaderOps(action.HeaderOps)
			continue
		default:
			if len(combined.HeaderOps) > 0 {
				action.HeaderOps = append(append([]switchboard.HeaderOp(nil), combined.HeaderOps...), action.HeaderOps...)
			}
			return action
		}
	}
	return combined
}
