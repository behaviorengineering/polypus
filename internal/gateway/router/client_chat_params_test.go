package router

import (
	"errors"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestChatParamsFromOpenAIBodyMaxTokens(t *testing.T) {
	t.Parallel()
	params, err := chatParamsFromOpenAIBody([]byte(`{"messages":[],"max_tokens":42}`))
	if err != nil {
		t.Fatal(err)
	}
	if params == nil || params.MaxCompletionTokens == nil || *params.MaxCompletionTokens != 42 {
		t.Fatalf("params: %+v", params)
	}
}

func TestChatParamsFromOpenAIBodyInvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := chatParamsFromOpenAIBody([]byte(`{`))
	if err == nil {
		t.Fatal("expected error")
	}
	var de *derrors.Error
	if !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
		t.Fatalf("err: %v", err)
	}
}
