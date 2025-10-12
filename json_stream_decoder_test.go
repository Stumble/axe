package axe

import (
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
		"code_output:\n",
		"<CodeOutput>\n  <Rewrite path=\"demo/add_test.go\"><![CDATA[\npackage demo_test\n\nimport (\n\t\"math/big\"\n\t\"testing\"\n\n\t\"github.com/stretchr/testify/assert\"\n\t\"github.com/stretchr/testify/suite\"\n\n\t\"demo\"\n)\n\n...\n</Rewrite>\n</CodeOutput>\n",
	}, chunks)
}

func TestJSONStreamDecoderErrorsOnUnsupportedTypes(t *testing.T) {
	input := `{"nested":{"value":1}}`

	decoder := NewJSONStreamDecoder(strings.NewReader(input))
	err := decoder.Stream(func(string) error { return nil })
	require.Error(t, err)
}
