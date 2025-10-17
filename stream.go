package axe

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/rs/zerolog/log"

	"github.com/stumble/axe/json_stream_decoder"
)

var ErrDecoderFailed = errors.New("axe: decoder failed")

type ToolCallStreamer struct {
	ID            string
	FnName        string
	Arguments     strings.Builder
	HasError      error
	Reader        *io.PipeReader
	Writer        *io.PipeWriter
	Decoder       *json_stream_decoder.JSONStreamDecoder
	Once          sync.Once
	HeaderPrinted bool

	Out chan<- string
}

func NewToolCallStreamer(id string, out chan<- string) *ToolCallStreamer {
	pr, pw := io.Pipe()
	s := &ToolCallStreamer{
		ID:      id,
		Reader:  pr,
		Writer:  pw,
		Decoder: json_stream_decoder.NewJSONStreamDecoder(pr),
		Out:     out,
	}
	// This goroutine will be closed when the pipe is closed, which happens
	// when the Close() method is called.
	go func() {
		err := s.Decoder.Stream(func(str string) error {
			s.Out <- str
			return nil
		})
		if err != nil {
			s.HasError = fmt.Errorf("%w: because %w", ErrDecoderFailed, err)
		}
		// Close the reader to signal writer that no one is consuming the stream anymore.
		if err != nil {
			log.Error().Err(err).Msg("axe: decoder failed")
			err = s.Reader.CloseWithError(err)
			if err != nil {
				log.Error().Err(err).Msg("axe: failed to close pipe.reader with error")
			}
		} else {
			err = s.Reader.Close()
			if err != nil {
				log.Error().Err(err).Msg("axe: failed to close pipe.reader")
			}
		}
	}()
	return s
}

func (s *ToolCallStreamer) Close() error {
	if s.HasError != nil {
		log.Warn().Err(s.HasError).Str("arguments", s.Arguments.String()).Msg("axe: tool call streamer failed to print arguments. NOTE: this does not affect the tool call execution.")
	}

	var err error
	s.Once.Do(func() {
		err = s.Writer.Close()
	})
	return err
}

func (s *ToolCallStreamer) OnMsg(call *schema.ToolCall) error {
	if call.Function.Name != "" {
		s.FnName += call.Function.Name
	}
	if call.Function.Arguments != "" {
		s.Arguments.WriteString(call.Function.Arguments)
		if !s.HeaderPrinted {
			s.HeaderPrinted = true
			s.Out <- fmt.Sprintf("\nTool call id: %s\n", s.ID)
			s.Out <- fmt.Sprintf("Tool call function name: %s\n", s.FnName)
			s.Out <- "Tool call arguments:\n"
		}
		_, err := s.Writer.Write([]byte(call.Function.Arguments))

		if err != nil {
			if !errors.Is(err, ErrDecoderFailed) {
				// reader is already closed due decoder error, we just silently ignore the error
				return nil
			}
		}
		return err
	}
	return nil
}
