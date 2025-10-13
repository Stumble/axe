package axe

import (
	"context"
	"errors"
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

// toolCallChecker observes streamed messages from the model and proxies them to the runner output channel.
func (r *Runner) toolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	hasToolCalls := false
	lastToolCallID := ""
	var callStreamer *ToolCallStreamer
	defer func() {
		if callStreamer != nil {
			_ = callStreamer.Close()
		}
	}()
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, err
		}
		log.Debug().Str("type", fmt.Sprintf("%T", msg)).Any("msg", msg).Msg("stream msg")

		if len(msg.ToolCalls) > 0 {
			hasToolCalls = true
			if len(msg.ToolCalls) > 1 {
				// XXX(yxia): we don't support stream multiple tool calls yet.
				// I am not even sure if model would stream multiple tool calls at once.
				// Even model has multiple tool calls, I assume it will return them one by one.
				// Anyways, in this case, we just simply stream the message.
				r.streamFrame(r.Output, msg)
			} else {
				call := msg.ToolCalls[0]
				if call.ID != "" && call.ID != lastToolCallID {
					// close the previous call streamer and create a new one
					if callStreamer != nil {
						_ = callStreamer.Close()
					}
					lastToolCallID = call.ID
					callStreamer = NewToolCallStreamer(call.ID, r.Output)
				}
				err := callStreamer.OnMsg(&call)
				if err != nil {
					// unexpected error, just return
					return false, err
				}
			}
		} else {
			r.streamFrame(r.Output, msg)
		}
	}
	r.Output <- "\n"
	return hasToolCalls, nil
}

func (r *Runner) streamFrame(out chan<- string, frame any) {
	switch frame := frame.(type) {
	case *schema.Message:
		if frame.Content != "" {
			out <- frame.Content
		} else if len(frame.ToolCalls) > 0 {
			panic("tool calls in message")
		}
	}
}
