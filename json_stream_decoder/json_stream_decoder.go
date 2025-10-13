package json_stream_decoder

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// JSONStreamDecoder incrementally decodes JSON objects and emits plain text representations of
// their values. The decoder is tolerant to truncated payloads and will emit any partial content it
// was able to decode before returning an error.
type JSONStreamDecoder struct {
	reader  *bufio.Reader
	emitted bool
}

// PartialJSONError indicates that the decoder encountered an error after emitting at least one
// chunk of output.
type PartialJSONError struct {
	Err error
}

// Error implements the error interface.
func (e *PartialJSONError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the wrapped error.
func (e *PartialJSONError) Unwrap() error {
	return e.Err
}

// Partial reports that some output was emitted before the error occurred.
func (e *PartialJSONError) Partial() bool {
	return true
}

// NewJSONStreamDecoder creates a JSONStreamDecoder that reads tokens from r.
func NewJSONStreamDecoder(r io.Reader) *JSONStreamDecoder {
	return &JSONStreamDecoder{reader: bufio.NewReader(r)}
}

// Stream parses the JSON object and invokes out for each chunk of plain text representation.
func (d *JSONStreamDecoder) Stream(out func(string) error) error {
	if err := d.skipSpaces(); err != nil {
		return d.wrapError(err)
	}
	if err := d.expectByte('{'); err != nil {
		return d.wrapError(err)
	}

	first := true
	for {
		if err := d.skipSpaces(); err != nil {
			return d.wrapError(err)
		}

		next, err := d.peekByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return d.wrapError(io.ErrUnexpectedEOF)
			}
			return d.wrapError(err)
		}

		if next == '}' {
			if _, err := d.reader.ReadByte(); err != nil {
				return d.wrapError(err)
			}
			break
		}

		if !first {
			if next != ',' {
				return d.wrapError(fmt.Errorf("expected ',' between object fields"))
			}
			if _, err := d.reader.ReadByte(); err != nil {
				return d.wrapError(err)
			}
			if err := d.skipSpaces(); err != nil {
				return d.wrapError(err)
			}
		}

		if err := d.expectByte('"'); err != nil {
			return d.wrapError(err)
		}
		key, err := d.readString()
		if err != nil {
			return d.wrapError(err)
		}
		if err := d.emit(out, fmt.Sprintf("%s:\n", key)); err != nil {
			return err
		}

		if err := d.skipSpaces(); err != nil {
			return d.wrapError(err)
		}
		if err := d.expectByte(':'); err != nil {
			return d.wrapError(err)
		}
		if err := d.skipSpaces(); err != nil {
			return d.wrapError(err)
		}

		if err := d.readValue(out); err != nil {
			return d.wrapError(err)
		}
		first = false
	}

	return nil
}

func (d *JSONStreamDecoder) emit(out func(string) error, chunk string) error {
	if err := out(chunk); err != nil {
		return err
	}
	d.emitted = true
	return nil
}

func (d *JSONStreamDecoder) wrapError(err error) error {
	if err == nil {
		return nil
	}
	if d.emitted {
		return &PartialJSONError{Err: err}
	}
	return err
}

func (d *JSONStreamDecoder) skipSpaces() error {
	for {
		b, err := d.peekByte()
		if err != nil {
			return err
		}
		if !isSpace(b) {
			return nil
		}
		if _, err := d.reader.ReadByte(); err != nil {
			return err
		}
	}
}

func (d *JSONStreamDecoder) expectByte(expected byte) error {
	b, err := d.reader.ReadByte()
	if err != nil {
		return err
	}
	if b != expected {
		return fmt.Errorf("expected '%c', got '%c'", expected, b)
	}
	return nil
}

func (d *JSONStreamDecoder) peekByte() (byte, error) {
	b, err := d.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (d *JSONStreamDecoder) readString() (string, error) {
	var sb strings.Builder
	for {
		b, err := d.reader.ReadByte()
		if err != nil {
			return sb.String(), err
		}
		if b == '"' {
			return sb.String(), nil
		}
		if b == '\\' {
			decoded, err := d.readEscape()
			if err != nil {
				return sb.String(), err
			}
			sb.WriteString(decoded)
			continue
		}
		sb.WriteByte(b)
	}
}

func (d *JSONStreamDecoder) readValue(out func(string) error) error {
	b, err := d.peekByte()
	if err != nil {
		return err
	}
	switch b {
	case '"':
		if _, err := d.reader.ReadByte(); err != nil {
			return err
		}
		return d.readStringValue(out)
	case '{', '[':
		return fmt.Errorf("unsupported json stream value type %c", b)
	default:
		return d.readLiteralValue(out)
	}
}

func (d *JSONStreamDecoder) readStringValue(out func(string) error) error {
	var sb strings.Builder
	for {
		b, err := d.reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if sb.Len() > 0 {
					if err := d.emit(out, ensureTrailingNewline(sb.String())); err != nil {
						return err
					}
				}
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if b == '"' {
			return d.emit(out, ensureTrailingNewline(sb.String()))
		}
		if b == '\\' {
			decoded, err := d.readEscape()
			sb.WriteString(decoded)
			if err != nil {
				if sb.Len() > 0 {
					if err := d.emit(out, ensureTrailingNewline(sb.String())); err != nil {
						return err
					}
				}
				return err
			}
			continue
		}
		sb.WriteByte(b)
	}
}

func (d *JSONStreamDecoder) readLiteralValue(out func(string) error) error {
	var sb strings.Builder
	for {
		b, err := d.reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if sb.Len() > 0 {
					return d.emit(out, ensureTrailingNewline(sb.String()))
				}
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if b == ',' || b == '}' || isSpace(b) {
			if b == ',' || b == '}' {
				if err := d.reader.UnreadByte(); err != nil {
					return err
				}
			} else {
				if err := d.skipSpaces(); err != nil {
					if errors.Is(err, io.EOF) {
						// whitespace exhausted, treat as success
						if sb.Len() == 0 {
							return io.ErrUnexpectedEOF
						}
						return d.emit(out, ensureTrailingNewline(sb.String()))
					}
					return err
				}
			}
			break
		}
		sb.WriteByte(b)
	}
	if sb.Len() == 0 {
		return io.ErrUnexpectedEOF
	}
	return d.emit(out, ensureTrailingNewline(sb.String()))
}

func (d *JSONStreamDecoder) readEscape() (string, error) {
	b, err := d.reader.ReadByte()
	if err != nil {
		return "\\", err
	}
	switch b {
	case '"', '\\', '/':
		return string(b), nil
	case 'b':
		return "\b", nil
	case 'f':
		return "\f", nil
	case 'n':
		return "\n", nil
	case 'r':
		return "\r", nil
	case 't':
		return "\t", nil
	case 'u':
		var hex [4]byte
		n, err := io.ReadFull(d.reader, hex[:])
		if err != nil {
			return "\\u" + string(hex[:n]), err
		}
		value, err := strconv.ParseUint(string(hex[:]), 16, 16)
		if err != nil {
			return "", err
		}
		r := rune(value)
		if utf16.IsSurrogate(r) {
			next, err := d.reader.Peek(2)
			if err == nil && next[0] == '\\' && next[1] == 'u' {
				// consume the escape marker
				if _, err := d.reader.ReadByte(); err != nil {
					return string(r), err
				}
				if _, err := d.reader.ReadByte(); err != nil {
					return string(r), err
				}
				var hex2 [4]byte
				n2, err := io.ReadFull(d.reader, hex2[:])
				if err != nil {
					return string(r) + "\\u" + string(hex2[:n2]), err
				}
				value2, err := strconv.ParseUint(string(hex2[:]), 16, 16)
				if err != nil {
					return "", err
				}
				sur := utf16.DecodeRune(r, rune(value2))
				if sur != utf8.RuneError {
					return string(sur), nil
				}
				return string(r) + string(rune(value2)), nil
			}
		}
		return string(r), nil
	default:
		return "", fmt.Errorf("invalid escape sequence \\%c", b)
	}
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}
