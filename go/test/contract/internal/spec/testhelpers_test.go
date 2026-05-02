package spec

import (
	"encoding/json"
	"os"
)

func marshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644)
}
