package switchboard

type ActionType string

const (
	ActionNext     ActionType = "next"
	ActionDeny     ActionType = "deny"
	ActionRedirect ActionType = "redirect"
	ActionRewrite  ActionType = "rewrite"
)

type Request struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
}

type Action struct {
	Type        ActionType        `json:"type"`
	StatusCode  int               `json:"status_code,omitempty"`
	Location    string            `json:"location,omitempty"`
	RewritePath string            `json:"rewrite_path,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}
