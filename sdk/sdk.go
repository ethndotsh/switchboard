package sdk

import "github.com/ethndotsh/switchboard"

type Request = switchboard.Request
type Action = switchboard.Action
type ActionType = switchboard.ActionType

const (
	ActionNext     = switchboard.ActionNext
	ActionDeny     = switchboard.ActionDeny
	ActionRedirect = switchboard.ActionRedirect
	ActionRewrite  = switchboard.ActionRewrite
)

func Next(req Request) Action {
	headers := map[string]string{}
	for key, values := range req.Headers {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return Action{Type: ActionNext, Headers: headers}
}

func Deny(status int) Action {
	if status == 0 {
		status = 403
	}
	return Action{Type: ActionDeny, StatusCode: status}
}

func Redirect(status int, location string) Action {
	if status == 0 {
		status = 302
	}
	return Action{Type: ActionRedirect, StatusCode: status, Location: location}
}

func Rewrite(req Request, path string) Action {
	return Action{Type: ActionRewrite, RewritePath: path}
}
