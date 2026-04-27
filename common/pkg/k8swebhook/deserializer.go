package k8swebhook

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	// RuntimeScheme is the scheme used by the webhook deserializer.
	RuntimeScheme = runtime.NewScheme()

	// Codecs is the codec factory used by the webhook deserializer.
	Codecs = serializer.NewCodecFactory(RuntimeScheme)

	// Deserializer is the deserializer used by the webhook.
	Deserializer = Codecs.UniversalDeserializer()

	// Defaulter is the object defaulter used by the webhook.
	Defaulter = runtime.ObjectDefaulter(RuntimeScheme)
)
