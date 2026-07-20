package guest

import "github.com/ethndotsh/switchboard/sdk"

func CurrentRequest() sdk.Request {
	return sdk.CurrentRequest()
}

func Return(action sdk.Action) int32 {
	return sdk.Return(action)
}

// Data accessors re-export the SDK's read-only bundled data files.
type Set = sdk.Set

func DataBytes(name string) []byte { return sdk.DataBytes(name) }

func DataString(name string) string { return sdk.DataString(name) }

func DataMap(name string) map[string]string { return sdk.DataMap(name) }

func DataSet(name string) Set { return sdk.DataSet(name) }

func DataLines(name string) []string { return sdk.DataLines(name) }

func DataJSON(name string, v any) error { return sdk.DataJSON(name, v) }

func DataJSONL(name string, out any) error { return sdk.DataJSONL(name, out) }

func DataTOML(name string, v any) error { return sdk.DataTOML(name, v) }

func DataCSV(name string) ([][]string, error) { return sdk.DataCSV(name) }
