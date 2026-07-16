package switchboard

type Decision string

const (
	DecisionNext     Decision = "next"
	DecisionDeny     Decision = "deny"
	DecisionRedirect Decision = "redirect"
	DecisionRewrite  Decision = "rewrite"
	DecisionRespond  Decision = "respond"
)

type HeaderOpType string

const (
	HeaderOpSet    HeaderOpType = "set"
	HeaderOpAdd    HeaderOpType = "add"
	HeaderOpDelete HeaderOpType = "delete"
)

type Request struct {
	Method     string              `json:"method"`
	Scheme     string              `json:"scheme,omitempty"`
	Host       string              `json:"host,omitempty"`
	Path       string              `json:"path"`
	RawQuery   string              `json:"raw_query,omitempty"`
	Protocol   string              `json:"protocol,omitempty"`
	Headers    map[string][]string `json:"headers"`
	RemoteAddr string              `json:"remote_addr,omitempty"`
	ClientIP   string              `json:"client_ip,omitempty"`
	TLS        bool                `json:"tls,omitempty"`
}

type HeaderOp struct {
	Op    HeaderOpType `json:"op"`
	Name  string       `json:"name"`
	Value string       `json:"value,omitempty"`
}

// RequestPatch mutates the request before it continues down the chain; nil
// pointer fields leave the component unchanged.
type RequestPatch struct {
	Headers []HeaderOp `json:"headers,omitempty"`
	Host    *string    `json:"host,omitempty"`
	Path    *string    `json:"path,omitempty"`
	Query   *string    `json:"query,omitempty"`
}

func (p RequestPatch) IsZero() bool {
	return len(p.Headers) == 0 && p.Host == nil && p.Path == nil && p.Query == nil
}

type Response struct {
	Status   int        `json:"status,omitempty"`
	Location string     `json:"location,omitempty"`
	Headers  []HeaderOp `json:"headers,omitempty"`
	Body     []byte     `json:"body,omitempty"`
}

type Action struct {
	Decision Decision          `json:"decision"`
	Patch    RequestPatch      `json:"patch,omitempty"`
	Response Response          `json:"response,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Reason   string            `json:"reason,omitempty"`
}
