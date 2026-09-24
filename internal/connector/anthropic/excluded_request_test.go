package anthropic

import (
	"errors"
	"testing"
)

func TestExcludedMandatoryTokenParameterCannotBeReintroduced(t *testing.T) {
	request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hello"}],"max_tokens":12}`)
	if err := request.ExcludeFields([]string{"max_tokens"}); err != nil {
		t.Fatal(err)
	}
	clone := request.CloneForAttempt()
	defer clone.Clear()
	if _, err := compileRequest(clone, "example", "nbu", BuiltInDefaultMaxTokens); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("mandatory field was silently restored", err)
	}
}
