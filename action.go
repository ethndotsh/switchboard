package switchboard

type ActionType string

const (
	ActionNext     ActionType = "next"
	ActionDeny     ActionType = "deny"
	ActionRedirect ActionType = "redirect"
	ActionRewrite  ActionType = "rewrite"
)

type HeaderOpType string

const (
	HeaderOpSet    HeaderOpType = "set"
	HeaderOpAdd    HeaderOpType = "add"
	HeaderOpDelete HeaderOpType = "delete"
)

type Request struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
}

type HeaderOp struct {
	Op    HeaderOpType `json:"op"`
	Name  string       `json:"name"`
	Value string       `json:"value,omitempty"`
}

type Action struct {
	Type        ActionType `json:"type"`
	StatusCode  int        `json:"status_code,omitempty"`
	Location    string     `json:"location,omitempty"`
	RewritePath string     `json:"rewrite_path,omitempty"`
	HeaderOps   []HeaderOp `json:"header_ops,omitempty"`
}
