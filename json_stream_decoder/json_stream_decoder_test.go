package json_stream_decoder

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONStreamDecoderStreamsPlainText(t *testing.T) {
	input := `{"code_output":"<CodeOutput>\n  <Rewrite path=\"demo/add_test.go\"><![CDATA[\npackage demo_test\n\nimport (\n\t\"math/big\"\n\t\"testing\"\n\n\t\"github.com/stretchr/testify/assert\"\n\t\"github.com/stretchr/testify/suite\"\n\n\t\"demo\"\n)\n\n...\n</Rewrite>\n</CodeOutput>"}`

	decoder := NewJSONStreamDecoder(strings.NewReader(input))
	var chunks []string
	err := decoder.Stream(func(s string) error {
		chunks = append(chunks, s)
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{
		"code_output:",
		"<CodeOutput>\n  <Rewrite path=\"demo/add_test.go\"><![CDATA[\npackage demo_test\n\nimport (\n\t\"math/big\"\n\t\"testing\"\n\n\t\"github.com/stretchr/testify/assert\"\n\t\"github.com/stretchr/testify/suite\"\n\n\t\"demo\"\n)\n\n...\n</Rewrite>\n</CodeOutput>",
	}, chunks)
}

func TestJSONStreamDecoderStreamsPartialTextOnEOF(t *testing.T) {
	input := `{"code_output":"partial value`

	decoder := NewJSONStreamDecoder(strings.NewReader(input))
	var chunks []string
	err := decoder.Stream(func(s string) error {
		chunks = append(chunks, s)
		return nil
	})

	require.Error(t, err)
	var partialErr *PartialJSONError
	require.ErrorAs(t, err, &partialErr)

	require.Equal(t, []string{
		"code_output:",
		"partial value",
	}, chunks)
}

func TestJSONStreamDecoderErrorsOnUnsupportedTypes(t *testing.T) {
	input := `{"nested":{"value":1}}`

	decoder := NewJSONStreamDecoder(strings.NewReader(input))
	err := decoder.Stream(func(string) error { return nil })
	require.Error(t, err)
}

func TestJSONStreamDecoderStreamsFromPipeBeforeClose(t *testing.T) {
	pr, pw := io.Pipe()
	decoder := NewJSONStreamDecoder(pr)

	chunksCh := make(chan string, 16)
	doneCh := make(chan error, 1)

	go func() {
		err := decoder.Stream(func(s string) error {
			chunksCh <- s
			return nil
		})
		doneCh <- err
		close(chunksCh)
	}()

	// Write the JSON up to the start of the string value so the key is emitted
	_, err := pw.Write([]byte(`{"code_output":"`))
	require.NoError(t, err)

	// Expect the key chunk
	keyChunk := <-chunksCh
	require.Equal(t, "code_output:", keyChunk)

	// Write a partial value but do not finish or close the pipe yet
	_, err = pw.Write([]byte("partial value"))
	require.NoError(t, err)

	// We should receive the partial value before the writer is closed
	select {
	case got := <-chunksCh:
		require.Equal(t, "partial value", got)
	default:
		t.Fatalf("expected partial chunk emission before closing the pipe")
	}

	// The stream should still be running at this point
	select {
	case err := <-doneCh:
		t.Fatalf("stream finished early: %v", err)
	default:
	}

	// Now finish the JSON value and close
	_, err = pw.Write([]byte(`"}`))
	require.NoError(t, err)
	require.NoError(t, pw.Close())

	// Ensure the stream finishes without error
	err = <-doneCh
	require.NoError(t, err)
}
