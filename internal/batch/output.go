package batch

import (
	"encoding/json"
	"net/http"
	"strings"
)

// LineResult is one finished batch line in the OpenAI shape, whatever the provider.
type LineResult struct {
	CustomID string
	// Body is the OpenAI response body (chat.completion or embedding list) when the
	// line succeeded.
	Body json.RawMessage
	// ErrCode and ErrMessage describe a line that did not succeed.
	ErrCode    string
	ErrMessage string
}

// outputLine is one line of an OpenAI batch output or error file.
type outputLine struct {
	ID       string       `json:"id"`
	CustomID string       `json:"custom_id"`
	Response *outputResp  `json:"response"`
	Error    *outputError `json:"error"`
}

type outputResp struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
}

type outputError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BuildOutput writes the OpenAI batch output and error JSONL. Every line lands in one
// of the two files, so completed + failed equals len(lines): a body that does not
// encode (invalid upstream JSON) becomes an output_encode_failed error line.
func BuildOutput(lines []LineResult) (okJSONL, errJSONL []byte, completed, failed int) {
	var okLines, errLines []string
	for _, item := range lines {
		customID := strings.TrimSpace(item.CustomID)
		line := outputLine{ID: "batch_req_" + customID, CustomID: customID}
		if len(item.Body) > 0 && item.ErrCode == "" {
			line.Response = &outputResp{StatusCode: http.StatusOK, Body: item.Body}
		} else {
			line.Error = &outputError{Code: item.ErrCode, Message: item.ErrMessage}
		}
		b, err := json.Marshal(line)
		if err != nil {
			line.Response = nil
			line.Error = &outputError{Code: "output_encode_failed", Message: err.Error()}
			if b, err = json.Marshal(line); err != nil {
				b = []byte(`{"error":{"code":"output_encode_failed","message":"line could not be encoded"}}`)
			}
		}
		if line.Response != nil {
			okLines = append(okLines, string(b))
			completed++
		} else {
			errLines = append(errLines, string(b))
			failed++
		}
	}
	if len(okLines) > 0 {
		okJSONL = []byte(strings.Join(okLines, "\n") + "\n")
	}
	if len(errLines) > 0 {
		errJSONL = []byte(strings.Join(errLines, "\n") + "\n")
	}
	return okJSONL, errJSONL, completed, failed
}
