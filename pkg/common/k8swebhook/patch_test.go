package k8swebhook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestMutatingWebhookPatch(t *testing.T) {
	testCases := []struct {
		name        string
		configs     admissionregistrationv1.MutatingWebhookConfigurationList
		configName  string
		webhookName string
		pemData     []byte
		err         string
	}{
		{
			"WebhookConfigNotFound",
			admissionregistrationv1.MutatingWebhookConfigurationList{},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"\"config1\" not found",
		},
		{
			"WebhookEntryNotFound",
			admissionregistrationv1.MutatingWebhookConfigurationList{
				Items: []admissionregistrationv1.MutatingWebhookConfiguration{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "config1",
						},
					},
				},
			},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"webhook entry \"webhook1\" not found in config \"config1\"",
		},
		{
			"SuccessfullyPatched",
			admissionregistrationv1.MutatingWebhookConfigurationList{
				Items: []admissionregistrationv1.MutatingWebhookConfiguration{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "config1",
						},
						Webhooks: []admissionregistrationv1.MutatingWebhook{
							{
								Name:         "webhook1",
								ClientConfig: admissionregistrationv1.WebhookClientConfig{},
							},
						},
					},
				},
			},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(tc.configs.DeepCopyObject())
			patcher := CreateCertPatcher("", tc.configName, tc.webhookName, nil)
			var v1Patcher = patcher.(*V1Patcher)
			err := v1Patcher.PatchMutatingWebhookConfig(client.AdmissionregistrationV1().MutatingWebhookConfigurations(), tc.pemData)

			if (err != nil) != (tc.err != "") {
				t.Fatalf("Wrong error: got %v want %v", err, tc.err)
			}

			if err != nil {
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Got %q, want %q", err, tc.err)
				}
			} else {
				config := admissionregistrationv1.MutatingWebhookConfiguration{}
				patch := client.Actions()[1].(k8stesting.PatchAction).GetPatch()
				err = json.Unmarshal(patch, &config)
				if err != nil {
					t.Fatalf("Fail to parse the patch: %s", err.Error())
				}
				if !bytes.Equal(config.Webhooks[0].ClientConfig.CABundle, tc.pemData) {
					t.Fatalf("Incorrect CA bundle: expect %s got %s", tc.pemData, config.Webhooks[0].ClientConfig.CABundle)
				}
			}
		})
	}
}

func TestValidatingWebhookPatch(t *testing.T) {
	testCases := []struct {
		name        string
		configs     admissionregistrationv1.ValidatingWebhookConfigurationList
		configName  string
		webhookName string
		pemData     []byte
		err         string
	}{
		{
			"WebhookConfigNotFound",
			admissionregistrationv1.ValidatingWebhookConfigurationList{},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"\"config1\" not found",
		},
		{
			"WebhookEntryNotFound",
			admissionregistrationv1.ValidatingWebhookConfigurationList{
				Items: []admissionregistrationv1.ValidatingWebhookConfiguration{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "config1",
						},
					},
				},
			},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"webhook entry \"webhook1\" not found in config \"config1\"",
		},
		{
			"SuccessfullyPatched",
			admissionregistrationv1.ValidatingWebhookConfigurationList{
				Items: []admissionregistrationv1.ValidatingWebhookConfiguration{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "config1",
						},
						Webhooks: []admissionregistrationv1.ValidatingWebhook{
							{
								Name:         "webhook1",
								ClientConfig: admissionregistrationv1.WebhookClientConfig{},
							},
						},
					},
				},
			},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(tc.configs.DeepCopyObject())
			patcher := CreateCertPatcher("", tc.configName, tc.webhookName, nil)
			var v1Patcher = patcher.(*V1Patcher)
			err := v1Patcher.PatchValidatingWebhookConfig(client.AdmissionregistrationV1().ValidatingWebhookConfigurations(), tc.pemData)

			if (err != nil) != (tc.err != "") {
				t.Fatalf("Wrong error: got %v want %v", err, tc.err)
			}

			if err != nil {
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Got %q, want %q", err, tc.err)
				}
			} else {
				config := admissionregistrationv1.ValidatingWebhookConfiguration{}
				patch := client.Actions()[1].(k8stesting.PatchAction).GetPatch()
				err = json.Unmarshal(patch, &config)
				if err != nil {
					t.Fatalf("Fail to parse the patch: %s", err.Error())
				}
				if !bytes.Equal(config.Webhooks[0].ClientConfig.CABundle, tc.pemData) {
					t.Fatalf("Incorrect CA bundle: expect %s got %s", tc.pemData, config.Webhooks[0].ClientConfig.CABundle)
				}
			}
		})
	}
}
