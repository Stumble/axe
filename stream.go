package axe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/rs/zerolog/log"
)

// toolCallChecker observes streamed messages from the model and proxies them to the runner output channel.
func (r *Runner) toolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	hasToolCalls := false
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, err
		}

		if len(msg.ToolCalls) > 0 {
			hasToolCalls = true
		}
		r.streamFrame(r.Output, msg)
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
			for _, toolCall := range frame.ToolCalls {
				if toolCall.ID != "" {
					out <- fmt.Sprintf("\nTool call id: %s\n", toolCall.ID)
				}
				if toolCall.Function.Name != "" {
					out <- fmt.Sprintf("Tool call function name: %s\n", toolCall.Function.Name)
					out <- "Tool call arguments:\n"
				}

				if strings.TrimSpace(toolCall.Function.Arguments) == "" {
					continue
				}

				decoder := NewJSONStreamDecoder(strings.NewReader(toolCall.Function.Arguments))
				if err := decoder.Stream(func(chunk string) error {
					out <- chunk
					return nil
				}); err != nil {
					var partialErr *PartialJSONError
					if errors.As(err, &partialErr) && partialErr.Partial() {
						log.Debug().Err(err).Msg("partially decoded tool arguments")
						continue
					}
					log.Debug().Err(err).Msg("failed to decode tool arguments")
					out <- toolCall.Function.Arguments
				}
			}
		}
	default:
		log.Debug().Str("type", fmt.Sprintf("%T", frame)).Msg("stream frame")
		log.Debug().Any("frame", frame).Msg("stream frame")
	}
}
