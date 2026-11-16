package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// LoopAction selects what a detected loop does.
type LoopAction int

const (
	// LoopStop ends the run with a *LoopError.
	LoopStop LoopAction = iota
	// LoopNudge feeds a course-correction back to the model and continues.
	LoopNudge
)

// LoopConfig configures loop detection. Zero thresholds disable a check;
// Window bounds the call-based windows.
type LoopConfig struct {
	Window         int
	RepeatedCall   int
	RepeatedResult int
	Alternating    int
	RepeatedText   int
	Action         LoopAction
}

type loopCall struct {
	name        string
	fingerprint string
	args        json.RawMessage
}

type loopResult struct {
	call   string
	result string
}

type loopState struct {
	cfg       LoopConfig
	calls     []loopCall
	results   []loopResult
	lastText  string
	textCount int
}

func newLoopState(cfg LoopConfig) *loopState {
	if cfg.Window <= 0 {
		cfg.Window = 10
	}
	return &loopState{cfg: cfg}
}

func (s *loopState) reset() {
	s.calls = nil
	s.results = nil
	s.lastText = ""
	s.textCount = 0
}

func (s *loopState) prospective(calls []model.ToolCall) []loopCall {
	out := make([]loopCall, 0, len(s.calls)+len(calls))
	out = append(out, s.calls...)
	for _, call := range calls {
		out = append(out, loopCall{
			name:        call.Name,
			fingerprint: toolCallFingerprint(call),
			args:        append(json.RawMessage(nil), call.Args...),
		})
	}
	return out
}

func (s *loopState) checkPre(calls []model.ToolCall) *LoopError {
	prospective := s.prospective(calls)
	if s.cfg.Alternating > 0 {
		if found, ok := alternatingLoop(prospective, s.cfg.Alternating, s.cfg.Window); ok {
			return found
		}
	}
	if s.cfg.RepeatedCall > 0 {
		if found, ok := repeatedCallLoop(prospective, s.cfg.RepeatedCall, s.cfg.Window); ok {
			return found
		}
	}
	return nil
}

func (s *loopState) commitCalls(calls []model.ToolCall) {
	s.calls = s.prospective(calls)
	s.trim()
}

func (s *loopState) commitResults(calls []model.ToolCall, messages []model.Message) *LoopError {
	if s.cfg.RepeatedResult == 0 {
		return nil
	}
	index := 0
	for _, message := range messages {
		if message.Role != model.RoleTool {
			continue
		}
		if index >= len(s.calls) {
			break
		}
		call := s.calls[index]
		index++
		s.results = append(s.results, loopResult{call: call.fingerprint, result: resultFingerprint(message)})
	}
	s.trim()
	if found, ok := repeatedResultLoop(s.results, s.cfg.RepeatedResult, s.cfg.Window, s.calls); ok {
		return found
	}
	return nil
}

func (s *loopState) checkText(text string) *LoopError {
	if s.cfg.RepeatedText == 0 || strings.TrimSpace(text) == "" {
		s.lastText, s.textCount = "", 0
		return nil
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	if normalized == s.lastText {
		s.textCount++
	} else {
		s.lastText, s.textCount = normalized, 1
	}
	if s.textCount > s.cfg.RepeatedText {
		return &LoopError{
			Pattern: string("repeated_text"), Count: s.textCount, Threshold: s.cfg.RepeatedText,
			Message: loopMessage(string("repeated_text"), nil, s.textCount, s.cfg.RepeatedText),
		}
	}
	return nil
}

func (s *loopState) trim() {
	if s.cfg.Window <= 0 {
		return
	}
	if len(s.calls) > s.cfg.Window {
		s.calls = s.calls[len(s.calls)-s.cfg.Window:]
	}
	if len(s.results) > s.cfg.Window {
		s.results = s.results[len(s.results)-s.cfg.Window:]
	}
}

func repeatedCallLoop(calls []loopCall, threshold, window int) (*LoopError, bool) {
	start := 0
	if window > 0 && len(calls) > window {
		start = len(calls) - window
	}
	counts := make(map[string]int)
	for _, call := range calls[start:] {
		counts[call.fingerprint]++
		if counts[call.fingerprint] > threshold {
			return &LoopError{
				Pattern: "repeated_call", ToolNames: []string{call.name}, ToolName: call.name,
				Args: call.args, Count: counts[call.fingerprint], Threshold: threshold,
				Message: loopMessage("repeated_call", []string{call.name}, counts[call.fingerprint], threshold),
			}, true
		}
	}
	return nil, false
}

func alternatingLoop(calls []loopCall, threshold, window int) (*LoopError, bool) {
	need := threshold * 2
	if len(calls) < need || (window > 0 && need > window) {
		return nil, false
	}
	recent := calls[len(calls)-need:]
	for i := 2; i < len(recent); i++ {
		if recent[i].fingerprint != recent[i-2].fingerprint {
			return nil, false
		}
	}
	if recent[0].fingerprint == recent[1].fingerprint {
		return nil, false
	}
	return &LoopError{
		Pattern: "alternating_calls", ToolNames: []string{recent[0].name, recent[1].name},
		Count: threshold, Threshold: threshold,
		Message: loopMessage("alternating_calls", []string{recent[0].name, recent[1].name}, threshold, threshold),
	}, true
}

func repeatedResultLoop(results []loopResult, threshold, window int, calls []loopCall) (*LoopError, bool) {
	start := 0
	if window > 0 && len(results) > window {
		start = len(results) - window
	}
	type key struct{ call, result string }
	counts := make(map[key]int)
	for _, result := range results[start:] {
		k := key(result)
		counts[k]++
		if counts[k] > threshold {
			var name string
			for _, call := range calls {
				if call.fingerprint == result.call {
					name = call.name
					break
				}
			}
			return &LoopError{
				Pattern: "repeated_result", ToolNames: []string{name}, ToolName: name,
				Count: counts[k], Threshold: threshold,
				Message: loopMessage("repeated_result", []string{name}, counts[k], threshold),
			}, true
		}
	}
	return nil, false
}

func resultFingerprint(message model.Message) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%t\\x00%s\\x00%s", message.Failed, message.ToolName, message.Content)))
	return hex.EncodeToString(hash[:])
}

func loopMessage(pattern string, toolNames []string, count, threshold int) string {
	names := make([]string, len(toolNames))
	for i, name := range toolNames {
		names[i] = safeToolName(name)
	}
	subject := "the run"
	if len(names) > 0 {
		subject = "tool " + strings.Join(names, " and ")
	}
	switch pattern {
	case "repeated_result":
		return fmt.Sprintf("%s returned the same result %d times (limit %d). Calling it again will not change the outcome. Change your approach, or explain what is blocking you.", subject, count, threshold)
	case "alternating_calls":
		return fmt.Sprintf("%s alternated in a repeating pattern %d times (limit %d). Change your approach, or explain what is blocking you.", subject, count, threshold)
	case "repeated_text":
		return fmt.Sprintf("The same final answer repeated %d times (limit %d). Change your approach or explain what is blocking you.", count, threshold)
	default:
		return fmt.Sprintf("%s was requested with the same arguments %d times (limit %d). Change your approach, or explain what is blocking you.", subject, count, threshold)
	}
}

func safeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func nudgeMessage(err *LoopError) model.Message {
	return model.Message{Role: model.RoleUser, Content: err.Message}
}

func nudgeToolResults(calls []model.ToolCall, message string) []model.Message {
	out := make([]model.Message, 0, len(calls))
	for _, call := range calls {
		out = append(out, model.Message{
			Role: model.RoleTool, ToolCallID: call.ID, ToolName: call.Name,
			Failed: true, Content: message,
		})
	}
	return out
}
