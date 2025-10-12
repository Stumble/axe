package axe

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// JSONStreamDecoder incrementally decodes JSON objects and emits plain text representations of
// their string values.
type JSONStreamDecoder struct {
	dec *json.Decoder
}

// NewJSONStreamDecoder creates a JSONStreamDecoder that reads tokens from r.
func NewJSONStreamDecoder(r io.Reader) *JSONStreamDecoder {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	return &JSONStreamDecoder{dec: dec}
}

// Stream parses the JSON object and invokes out for each chunk of plain text representation.
func (d *JSONStreamDecoder) Stream(out func(string) error) error {
	token, err := d.dec.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("json stream must begin with an object")
	}

	for d.dec.More() {
		keyTok, err := d.dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("json stream object keys must be strings, got %T", keyTok)
		}
		if err := out(fmt.Sprintf("%s:\n", key)); err != nil {
			return err
		}

		valueTok, err := d.dec.Token()
		if err != nil {
			return err
		}

		switch value := valueTok.(type) {
		case string:
			chunk := value
			if !strings.HasSuffix(chunk, "\n") {
				chunk += "\n"
			}
			if err := out(chunk); err != nil {
				return err
			}
		case json.Number, bool:
			if err := out(fmt.Sprintf("%v\n", value)); err != nil {
				return err
			}
		case nil:
			if err := out("null\n"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported json stream value type %T", value)
		}
	}

	_, err = d.dec.Token() // consume '}'
	return err
}
