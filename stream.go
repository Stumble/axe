package axe

import (
	"fmt"
	"io"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/rs/zerolog/log"

	"github.com/stumble/axe/json_stream_decoder"
)

type ToolCallStreamer struct {
	ID            string
	FnName        string
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
			log.Error().Err(err).Msg("axe: tool call streamer stream")
		}
	}()
	return s
}

func (s *ToolCallStreamer) Close() error {
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
		if !s.HeaderPrinted {
			s.HeaderPrinted = true
			s.Out <- fmt.Sprintf("\nTool call id: %s\n", s.ID)
			s.Out <- fmt.Sprintf("Tool call function name: %s\n", s.FnName)
			s.Out <- "Tool call arguments:\n"
		}
		_, err := s.Writer.Write([]byte(call.Function.Arguments))
		return err
	}
	return nil
}
