package k8swebhook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	admissionregistrationbeta1v1 "k8s.io/api/admissionregistration/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestMutatingWebhookPatchBeta1V1(t *testing.T) {
	testCases := []struct {
		name        string
		configs     admissionregistrationbeta1v1.MutatingWebhookConfigurationList
		configName  string
		webhookName string
		pemData     []byte
		err         string
	}{
		{
			"WebhookConfigNotFound",
			admissionregistrationbeta1v1.MutatingWebhookConfigurationList{},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"\"config1\" not found",
		},
		{
			"WebhookEntryNotFound",
			admissionregistrationbeta1v1.MutatingWebhookConfigurationList{
				Items: []admissionregistrationbeta1v1.MutatingWebhookConfiguration{
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
			admissionregistrationbeta1v1.MutatingWebhookConfigurationList{
				Items: []admissionregistrationbeta1v1.MutatingWebhookConfiguration{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "config1",
						},
						Webhooks: []admissionregistrationbeta1v1.MutatingWebhook{
							{
								Name:         "webhook1",
								ClientConfig: admissionregistrationbeta1v1.WebhookClientConfig{},
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
			patcher := CreateCertPatcher("", tc.configName, tc.webhookName, time.Second, time.Second, nil, true)
			var beta1V1Patcher *V1Beta1Patcher = patcher.(*V1Beta1Patcher)
			err := beta1V1Patcher.PatchMutatingWebhookConfigForV1Beta1(client.AdmissionregistrationV1beta1().MutatingWebhookConfigurations(), tc.pemData)

			if (err != nil) != (tc.err != "") {
				t.Fatalf("Wrong error: got %v want %v", err, tc.err)
			}

			if err != nil {
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Got %q, want %q", err, tc.err)
				}
			} else {
				config := admissionregistrationbeta1v1.MutatingWebhookConfiguration{}
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

func TestValidatingWebhookPatchBeta1V1(t *testing.T) {
	testCases := []struct {
		name        string
		configs     admissionregistrationbeta1v1.ValidatingWebhookConfigurationList
		configName  string
		webhookName string
		pemData     []byte
		err         string
	}{
		{
			"WebhookConfigNotFound",
			admissionregistrationbeta1v1.ValidatingWebhookConfigurationList{},
			"config1",
			"webhook1",
			[]byte("fake CA"),
			"\"config1\" not found",
		},
		{
			"WebhookEntryNotFound",
			admissionregistrationbeta1v1.ValidatingWebhookConfigurationList{
				Items: []admissionregistrationbeta1v1.ValidatingWebhookConfiguration{
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
			admissionregistrationbeta1v1.ValidatingWebhookConfigurationList{
				Items: []admissionregistrationbeta1v1.ValidatingWebhookConfiguration{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "config1",
						},
						Webhooks: []admissionregistrationbeta1v1.ValidatingWebhook{
							{
								Name:         "webhook1",
								ClientConfig: admissionregistrationbeta1v1.WebhookClientConfig{},
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
			patcher := CreateCertPatcher("", tc.configName, tc.webhookName, time.Second, time.Second, nil, true)
			var beta1V1Patcher *V1Beta1Patcher = patcher.(*V1Beta1Patcher)
			err := beta1V1Patcher.PatchValidatingWebhookConfigForV1Beta1(client.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations(), tc.pemData)

			if (err != nil) != (tc.err != "") {
				t.Fatalf("Wrong error: got %v want %v", err, tc.err)
			}

			if err != nil {
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Got %q, want %q", err, tc.err)
				}
			} else {
				config := admissionregistrationbeta1v1.ValidatingWebhookConfiguration{}
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
