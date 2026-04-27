package templating

import (
	"fmt"
	"strings"

	"github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
)

// TemplateMessage represents a user-facing error emitted by a template.
// Typically, represent an issue with input/setup by the template itself.
type TemplateMessage struct {
	TemplateName string
	Message      string
}

// MessageCollector accumulates messages emitted by templates during a render cycle
// Not safe for concurrent use.
type MessageCollector struct {
	currentTemplate string
	messages        []TemplateMessage
}

func NewMessageCollector() *MessageCollector {
	return &MessageCollector{}
}

func (c *MessageCollector) SetCurrentTemplate(name string) {
	c.currentTemplate = name
}

func (c *MessageCollector) collect(message string) {
	c.messages = append(c.messages, TemplateMessage{
		TemplateName: c.currentTemplate,
		Message:      message,
	})
}

// Messages returns a copy of all collected messages, or nil if none were collected.
func (c *MessageCollector) Messages() []TemplateMessage {
	if len(c.messages) == 0 {
		return nil
	}
	out := make([]TemplateMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

// registerMessageFunction registers the msg_error native function on the
// jsonnet VM. It takes (message string, value any) and returns value unchanged
func registerMessageFunction(vm *jsonnet.VM, collector *MessageCollector) {
	vm.NativeFunction(&jsonnet.NativeFunction{
		Name:   "msg_error",
		Params: ast.Identifiers{"message", "value"},
		Func: func(args []interface{}) (interface{}, error) {
			message, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("msg_error: message argument must be a string, got %T", args[0])
			}
			collector.collect(message)
			return args[1], nil
		},
	})
}

// FormatMessages formats all collected messages into a single string suitable
// for inclusion in a UserConfigError or MOP status message.
func FormatMessages(messages []TemplateMessage) string {
	var parts []string
	for _, m := range messages {
		if m.TemplateName != "" {
			parts = append(parts, fmt.Sprintf("[%s] %s", m.TemplateName, m.Message))
		} else {
			parts = append(parts, m.Message)
		}
	}
	return strings.Join(parts, "; ")
}
