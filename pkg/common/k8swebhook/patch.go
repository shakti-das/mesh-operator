package k8swebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"time"

	v1 "k8s.io/api/admissionregistration/v1"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	admissionregistrationv1client "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type CertPatcher interface {
	Start(stopCh <-chan struct{}, patchValidatingWebhook bool,
		patchMutatingWebhook bool) error
}

func CreateCertPatcher(caCertFile string, webhookConfigName string, webhookName string, sugar *zap.SugaredLogger) CertPatcher {
	return NewV1Patcher(caCertFile, webhookConfigName, webhookName, time.Second, sugar)
}

type V1Patcher struct {
	caCertFile        string
	webhookConfigName string
	webhookName       string
	delayedRetryTime  time.Duration
	logger            *zap.SugaredLogger
}

func NewV1Patcher(caCertFile string, webhookConfigName string, webhookName string, delayedRetryTime time.Duration, logger *zap.SugaredLogger) CertPatcher {
	return &V1Patcher{
		caCertFile:        caCertFile,
		webhookConfigName: webhookConfigName,
		webhookName:       webhookName,
		delayedRetryTime:  delayedRetryTime,
		logger:            logger,
	}
}

func (p *V1Patcher) Start(stopCh <-chan struct{}, patchValidatingWebhook bool,
	patchMutatingWebhook bool) error {
	if patchValidatingWebhook {
		err := p.StartValidatingWebhookPatcher(stopCh)
		if err != nil {
			return err
		}
	}
	if patchMutatingWebhook {
		err := p.StartMutatingWebhookPatcher(stopCh)
		if err != nil {
			return err
		}
	}

	return nil
}

// PatchMutatingWebhookConfig patches a CA bundle into the specified mutating webhook config.
// Originally taken from istio: https://github.com/istio/istio/blob/625bb9c50ce141596ec7b2a2c240782b87a18aa6/pkg/util/webhookpatch.go
func (p *V1Patcher) PatchMutatingWebhookConfig(client admissionregistrationv1client.MutatingWebhookConfigurationInterface, caBundle []byte) error {
	config, err := client.Get(context.TODO(), p.webhookConfigName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	prev, err := json.Marshal(config)
	if err != nil {
		return err
	}

	found := false
	for i, w := range config.Webhooks {
		if w.Name == p.webhookName {
			config.Webhooks[i].ClientConfig.CABundle = caBundle[:]
			found = true
			break
		}
	}

	if !found {
		return apierrors.NewInternalError(fmt.Errorf("webhook entry %q not found in config %q", p.webhookName, p.webhookConfigName))
	}

	curr, err := json.Marshal(config)
	if err != nil {
		return err
	}

	patch, err := strategicpatch.CreateTwoWayMergePatch(prev, curr, v1.MutatingWebhookConfiguration{})
	if err != nil {
		return err
	}

	if string(patch) != "{}" {
		_, err = client.Patch(context.TODO(), p.webhookConfigName, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	}

	return err
}

// PatchValidatingWebhookConfig patches a CA bundle into the specified validating webhook config.
// Originally taken from istio: https://github.com/istio/istio/blob/625bb9c50ce141596ec7b2a2c240782b87a18aa6/pkg/util/webhookpatch.go
func (p *V1Patcher) PatchValidatingWebhookConfig(client admissionregistrationv1client.ValidatingWebhookConfigurationInterface, caBundle []byte) error {
	config, err := client.Get(context.TODO(), p.webhookConfigName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	prev, err := json.Marshal(config)
	if err != nil {
		return err
	}

	found := false
	for i, w := range config.Webhooks {
		if w.Name == p.webhookName {
			config.Webhooks[i].ClientConfig.CABundle = caBundle[:]
			found = true
			break
		}
	}

	if !found {
		return apierrors.NewInternalError(fmt.Errorf("webhook entry %q not found in config %q", p.webhookName, p.webhookConfigName))
	}

	curr, err := json.Marshal(config)
	if err != nil {
		return err
	}

	patch, err := strategicpatch.CreateTwoWayMergePatch(prev, curr, v1.ValidatingWebhookConfiguration{})
	if err != nil {
		return err
	}

	if string(patch) != "{}" {
		_, err = client.Patch(context.TODO(), p.webhookConfigName, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	}

	return err
}

// StartMutatingWebhookPatcher starts a file watcher on the given CA cert and patches the CA bundle into the specified
// mutating webhook config.
func (p *V1Patcher) StartMutatingWebhookPatcher(stopCh <-chan struct{}) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	caBundle, err := ioutil.ReadFile(p.caCertFile)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Do not watch the entire directory.
	// When cert file gets edited there are multiple .swp (tmp files) generated in the directory.
	err = watcher.Add(p.caCertFile)
	if err != nil {
		return fmt.Errorf("could not watch %v: %w", p.caCertFile, err)
	}
	retry := p.doMutatingWebhookPatch(client, caBundle)

	shouldPatch := make(chan struct{})

	watchlist := cache.NewListWatchFromClient(
		client.AdmissionregistrationV1().RESTClient(),
		"mutatingwebhookconfigurations",
		"",
		fields.ParseSelectorOrDie(fmt.Sprintf("metadata.name=%s", p.webhookConfigName)))

	_, controller := cache.NewInformer(
		watchlist,
		&v1.MutatingWebhookConfiguration{},
		0,
		cache.ResourceEventHandlerFuncs{
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldConfig := oldObj.(*v1.MutatingWebhookConfiguration)
				newConfig := newObj.(*v1.MutatingWebhookConfiguration)

				if oldConfig.ResourceVersion != newConfig.ResourceVersion {
					for i, w := range newConfig.Webhooks {
						if w.Name == p.webhookName && !bytes.Equal(newConfig.Webhooks[i].ClientConfig.CABundle, caBundle) {
							p.log("detected caBundle change in webhook config, re-patching")
							p.logger.Infow("caBundle change",
								"oldCaBundle", string(newConfig.Webhooks[i].ClientConfig.CABundle),
								"newCaBundle", string(caBundle))
							shouldPatch <- struct{}{}
							break
						}
					}
				}
			},
		},
	)
	go controller.Run(stopCh)

	go func() {
		var delayedRetryC <-chan time.Time
		if retry {
			delayedRetryC = time.After(p.delayedRetryTime)
		}

		for {
			select {
			case <-delayedRetryC:
				if retry := p.doMutatingWebhookPatch(client, caBundle); retry {
					delayedRetryC = time.After(p.delayedRetryTime)
				} else {
					p.log("retried patch succeeded")
					delayedRetryC = nil
				}

			case <-shouldPatch:
				p.doMutatingWebhookPatch(client, caBundle)

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// We only care about updates that change the file content
				if !(isWrite(event) || isRemove(event) || isCreate(event)) {
					continue
				}
				p.logger.Infow("patching signal received - file event", "event", event)
				if b, err := ioutil.ReadFile(p.caCertFile); err == nil {
					p.log("detected change in CA cert file, patching webhook config")
					caBundle = b
					p.doMutatingWebhookPatch(client, caBundle)
				} else {
					p.log("failed to read CA cert file", err)
				}
			case <-stopCh:
				p.logger.Infow("closing watcher from mwc....")
				_ = watcher.Close()
			}
		}
	}()

	return nil
}

// StartValidatingWebhookPatcher starts a file watcher on the given CA cert and patches the CA bundle into the specified
// validating webhook config.
func (p *V1Patcher) StartValidatingWebhookPatcher(stopCh <-chan struct{}) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	caBundle, err := ioutil.ReadFile(p.caCertFile)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Do not watch the entire directory.
	// When cert file gets edited there are multiple .swp (tmp files) generated in the directory.
	err = watcher.Add(p.caCertFile)
	if err != nil {
		return fmt.Errorf("could not watch %v: %w", p.caCertFile, err)
	}
	retry := p.doValidatingWebhookPatch(client, caBundle)

	shouldPatch := make(chan struct{})

	watchlist := cache.NewListWatchFromClient(
		client.AdmissionregistrationV1().RESTClient(),
		"validatingwebhookconfigurations",
		"",
		fields.ParseSelectorOrDie(fmt.Sprintf("metadata.name=%s", p.webhookConfigName)))

	_, controller := cache.NewInformer(
		watchlist,
		&v1.ValidatingWebhookConfiguration{},
		0,
		cache.ResourceEventHandlerFuncs{
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldConfig := oldObj.(*v1.ValidatingWebhookConfiguration)
				newConfig := newObj.(*v1.ValidatingWebhookConfiguration)

				if oldConfig.ResourceVersion != newConfig.ResourceVersion {
					for i, w := range newConfig.Webhooks {
						if w.Name == p.webhookName && !bytes.Equal(newConfig.Webhooks[i].ClientConfig.CABundle, caBundle) {
							p.log("detected caBundle change in validating webhook config, re-patching")
							p.logger.Infow("caBundle change",
								"oldCaBundle", string(newConfig.Webhooks[i].ClientConfig.CABundle),
								"newCaBundle", string(caBundle))
							shouldPatch <- struct{}{}
							break
						}
					}
				}
			},
		},
	)
	go controller.Run(stopCh)

	go func() {
		var delayedRetryC <-chan time.Time
		if retry {
			delayedRetryC = time.After(p.delayedRetryTime)
		}

		for {
			select {
			case <-delayedRetryC:
				p.log("patching signal received - delayed retry channel")
				if retry := p.doValidatingWebhookPatch(client, caBundle); retry {
					delayedRetryC = time.After(p.delayedRetryTime)
				} else {
					p.log("retried validating webhook patch succeeded")
					delayedRetryC = nil
				}

			case <-shouldPatch:
				p.log("patching signal received - should patch channel")
				p.doValidatingWebhookPatch(client, caBundle)

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// We only care about updates that change the file content
				if !(isWrite(event) || isRemove(event) || isCreate(event)) {
					continue
				}
				p.logger.Infow("patching signal received - file event", "event", event)
				if b, err := ioutil.ReadFile(p.caCertFile); err == nil {
					p.log("detected change in CA cert file, patching validating webhook config")
					caBundle = b
					p.doValidatingWebhookPatch(client, caBundle)
				} else {
					p.log("failed to read CA cert file", err)
				}
			case <-stopCh:
				p.logger.Infow("closing watcher from vwc....")
				_ = watcher.Close()
			}
		}
	}()

	return nil
}

func isWrite(event fsnotify.Event) bool {
	return event.Op&fsnotify.Write == fsnotify.Write
}

func isCreate(event fsnotify.Event) bool {
	return event.Op&fsnotify.Create == fsnotify.Create
}

func isRemove(event fsnotify.Event) bool {
	return event.Op&fsnotify.Remove == fsnotify.Remove
}

func (p *V1Patcher) doMutatingWebhookPatch(client *kubernetes.Clientset, caBundle []byte) (retry bool) {
	if err := p.PatchMutatingWebhookConfig(client.AdmissionregistrationV1().MutatingWebhookConfigurations(), caBundle); err != nil {
		p.log("failed to patch mutating webhook", err)
		return true
	}
	p.log("patched mutating webhook")
	return false
}

func (p *V1Patcher) doValidatingWebhookPatch(client *kubernetes.Clientset, caBundle []byte) (retry bool) {
	if err := p.PatchValidatingWebhookConfig(client.AdmissionregistrationV1().ValidatingWebhookConfigurations(), caBundle); err != nil {
		p.log("failed to patch validating webhook", err)
		return true
	}
	p.log("patched validating webhook")
	return false
}

func (p *V1Patcher) log(msg string, err ...error) {
	if err != nil {
		p.logger.Errorw(
			msg,
			"error", err,
			"caCertFile", p.caCertFile,
			"webhookConfigName", p.webhookConfigName,
			"webhookName", p.webhookName)
	} else {
		p.logger.Infow(
			msg,
			"caCertFile", p.caCertFile,
			"webhookConfigName", p.webhookConfigName,
			"webhookName", p.webhookName)
	}
}
