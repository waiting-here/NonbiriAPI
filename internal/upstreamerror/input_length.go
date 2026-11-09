package upstreamerror

import (
	"bytes"
	"encoding/json"
	"regexp"
)

// Generic value errors need an explicit input-length statement. The parameter
// alone cannot distinguish a length limit from malformed messages or content.
var inputLengthMessage = regexp.MustCompile(`(?i)` +
	`(?:输入(?:内容)?|消息|提示词|上下文|文本|字符串)\s*(?:的)?(?:总)?(?:字符数|字数|长度)\s*(?:已|已经)?(?:超出|超过|超限|过长|不能超过|不得超过)` +
	`|(?:输入(?:内容)?|消息|提示词|上下文|文本|字符串)\s*(?:太长|过长)` +
	`|\b(?:input|prompt|messages?|context|text|string)(?:\s+length)?\s+(?:is\s+)?too\s+long\b` +
	`|\b(?:input|prompt|messages?|context|text|string)\s+(?:length|token count|character count)\s+(?:exceeds?|exceeded)\b` +
	`|\b(?:input|prompt|messages?|context|text|string)\s+(?:exceeds?|exceeded)\s+(?:the\s+)?(?:maximum|max|limit)\b[^.\r\n!?]{0,64}\b(?:tokens?|characters?)\b` +
	`|\bmaximum\s+context\s+length\s+is\s+[0-9]+\s+tokens?\b`)

func inputLengthError(kind, code string, rawParam json.RawMessage, message string) bool {
	if kind != "invalid_request_error" {
		return false
	}
	var param string
	hasParam := len(rawParam) != 0 && !bytes.Equal(bytes.TrimSpace(rawParam), []byte("null"))
	if hasParam && json.Unmarshal(rawParam, &param) != nil {
		return false
	}
	inputParam := param == "messages" || param == "input" || param == "prompt"
	if hasParam && !inputParam {
		return false
	}
	switch code {
	case "context_length_exceeded", "input_too_long", "prompt_too_long":
		return true
	case "string_above_max_length":
		return inputParam
	case "invalid_value":
		return inputParam && inputLengthMessage.MatchString(message)
	}
	return false
}
