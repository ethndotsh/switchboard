package sdk

type Rule func(Request) Action

func Chain(req Request, rules ...Rule) Action {
	current := req
	for _, rule := range rules {
		action := rule(current)
		switch action.Type {
		case "", ActionNext:
			for key, value := range action.Headers {
				current.Headers[key] = []string{value}
			}
			continue
		default:
			return action
		}
	}
	return Next(current)
}
