package templating

import (
	"testing"

	"github.com/google/go-jsonnet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageCollector_Basic(t *testing.T) {
	c := NewMessageCollector()

	c.SetCurrentTemplate("filters_faultinjection")
	c.collect("missing required label")

	c.SetCurrentTemplate("filters_oauth")
	c.collect("port out of range")

	msgs := c.Messages()
	require.Len(t, msgs, 2)

	assert.Equal(t, "missing required label", msgs[0].Message)
	assert.Equal(t, "filters_faultinjection", msgs[0].TemplateName)

	assert.Equal(t, "port out of range", msgs[1].Message)
	assert.Equal(t, "filters_oauth", msgs[1].TemplateName)
}

func TestMessageCollector_MessagesReturnsCopy(t *testing.T) {
	c := NewMessageCollector()
	c.collect("one")

	msgs := c.Messages()
	require.Len(t, msgs, 1)

	c.collect("two")
	assert.Len(t, msgs, 1, "returned slice must not grow when collector gets new messages")
	assert.Len(t, c.Messages(), 2)
}

func TestMessageCollector_Empty(t *testing.T) {
	c := NewMessageCollector()
	msgs := c.Messages()
	assert.Nil(t, msgs)
}

func TestFormatMessages(t *testing.T) {
	msgs := []TemplateMessage{
		{TemplateName: "tmpl_a", Message: "port out of range"},
		{TemplateName: "tmpl_b", Message: "missing label"},
	}
	result := FormatMessages(msgs)
	assert.Equal(t, "[tmpl_a] port out of range; [tmpl_b] missing label", result)
}

func TestFormatMessages_Empty(t *testing.T) {
	assert.Equal(t, "", FormatMessages(nil))
}

func TestFormatMessages_NoTemplateName(t *testing.T) {
	msgs := []TemplateMessage{{Message: "bare message"}}
	assert.Equal(t, "bare message", FormatMessages(msgs))
}

func TestRegisterMessageFunction_Passthrough(t *testing.T) {
	collector := NewMessageCollector()
	collector.SetCurrentTemplate("test.jsonnet")

	vm := jsonnet.MakeVM()
	registerMessageFunction(vm, collector)

	out, err := vm.EvaluateAnonymousSnippet("test.jsonnet",
		`std.native("msg_error")("a fatal error", [1, 2, 3])`)
	require.NoError(t, err)
	assert.JSONEq(t, `[1,2,3]`, out)

	msgs := collector.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "a fatal error", msgs[0].Message)
	assert.Equal(t, "test.jsonnet", msgs[0].TemplateName)
}

func TestRegisterMessageFunction_NonStringMessageError(t *testing.T) {
	collector := NewMessageCollector()
	vm := jsonnet.MakeVM()
	registerMessageFunction(vm, collector)

	_, err := vm.EvaluateAnonymousSnippet("bad.jsonnet",
		`std.native("msg_error")(42, "value")`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message argument must be a string")
}

func TestRegisterMessageFunction_MissingSecondArg(t *testing.T) {
	collector := NewMessageCollector()
	vm := jsonnet.MakeVM()
	registerMessageFunction(vm, collector)

	_, err := vm.EvaluateAnonymousSnippet("bad.jsonnet",
		`std.native("msg_error")("only one arg")`)
	require.Error(t, err)
}

func TestRegisterMessageFunction_PassthroughPreservesComplexObjects(t *testing.T) {
	collector := NewMessageCollector()
	vm := jsonnet.MakeVM()
	registerMessageFunction(vm, collector)

	out, err := vm.EvaluateAnonymousSnippet("real.jsonnet", `
		local resources = [{
			apiVersion: "networking.istio.io/v1alpha3",
			kind: "EnvoyFilter",
			metadata: { name: "test-filter", namespace: "test-ns" },
		}];
		std.native("msg_error")("config issue detected", resources)
	`)
	require.NoError(t, err)
	assert.Contains(t, out, `"kind": "EnvoyFilter"`)
	assert.Contains(t, out, `"name": "test-filter"`)

	msgs := collector.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "config issue detected", msgs[0].Message)
}

func TestRegisterMessageFunction_EmptyArrayPassthrough(t *testing.T) {
	collector := NewMessageCollector()
	vm := jsonnet.MakeVM()
	registerMessageFunction(vm, collector)

	out, err := vm.EvaluateAnonymousSnippet("error_path.jsonnet",
		`std.native("msg_error")("service has no ports", [])`)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, out)

	msgs := collector.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "service has no ports", msgs[0].Message)
}

func TestRegisterMessageFunction_ConditionalUsage(t *testing.T) {
	collector := NewMessageCollector()
	vm := jsonnet.MakeVM()
	registerMessageFunction(vm, collector)

	out, err := vm.EvaluateAnonymousSnippet("cond.jsonnet", `
		local bad = true;
		if bad then
			std.native("msg_error")("something is wrong", [])
		else
			[{kind: "EnvoyFilter"}]
	`)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, out)
	require.Len(t, collector.Messages(), 1)
}

func TestRegisterMessageFunction_NoMessageWhenConditionFalse(t *testing.T) {
	collector := NewMessageCollector()
	vm := jsonnet.MakeVM()
	registerMessageFunction(vm, collector)

	out, err := vm.EvaluateAnonymousSnippet("cond.jsonnet", `
		local bad = false;
		if bad then
			std.native("msg_error")("something is wrong", [])
		else
			[{kind: "EnvoyFilter"}]
	`)
	require.NoError(t, err)
	assert.Contains(t, out, `"kind": "EnvoyFilter"`)
	assert.Empty(t, collector.Messages())
}
