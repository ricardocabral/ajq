package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
)

// Options controls jq-like serialization of evaluated values.
type Options struct {
	Compact bool
	Raw     bool
}

// WriteValue serializes one evaluated value followed by a newline.
func WriteValue(w io.Writer, value any, opts Options) error {
	encoded, err := Marshal(value, opts)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}

// Marshal serializes one value using jq-compatible JSON and raw-output rules.
func Marshal(value any, opts Options) ([]byte, error) {
	if opts.Raw {
		if s, ok := value.(string); ok {
			return []byte(s), nil
		}
	}

	encoded, err := gojq.Marshal(value)
	if err != nil {
		return nil, err
	}
	if opts.Compact {
		return encoded, nil
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encoded, "", "  "); err != nil {
		return nil, err
	}
	return pretty.Bytes(), nil
}
