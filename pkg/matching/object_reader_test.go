package matching

import (
	"testing"

	kubeError "k8s.io/apimachinery/pkg/api/errors"

	"go.uber.org/zap/zaptest"

	"k8s.io/client-go/tools/cache"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
)

var (
	testService = kube_test.NewServiceBuilder("my-service", "my-namespace").SetLabels(map[string]string{"app": "shipping"}).Build()
	testSe      = kube_test.NewServiceEntryBuilder("my-se", "my-namespace").SetLabels(map[string]string{"app": "shipping"}).Build()

	testClient = kube_test.NewKubeClientBuilder().AddK8sObjects(&testService).AddIstioObjects(testSe).Build()

	requirement, _ = labels.NewRequirement("app", selection.Equals, []string{"shipping"})
	selector       = labels.NewSelector().Add(*requirement)
)

func TestCreateSerivceReader_informerDisabled(t *testing.T) {
	reader := CreateServiceReader(zaptest.NewLogger(t).Sugar(), testClient)

	assert.IsType(t, &apiServiceReader{}, reader)
}

func TestCreateSerivceReader_informerEnabled(t *testing.T) {
	features.UseInformerToEnqueueSvcSe = true
	defer func() {
		features.UseInformerToEnqueueSvcSe = false
	}()

	reader := CreateServiceReader(zaptest.NewLogger(t).Sugar(), testClient)

	assert.IsType(t, &informerServiceReader{}, reader)
}

func TestCreateServiceEntryReader_informerDisabled(t *testing.T) {
	reader := CreateServiceEntryReader(zaptest.NewLogger(t).Sugar(), testClient)

	assert.IsType(t, &apiServiceEntryReader{}, reader)
}

func TestCreateServiceEntryReader_informerEnabled(t *testing.T) {
	features.UseInformerToEnqueueSvcSe = true
	defer func() {
		features.UseInformerToEnqueueSvcSe = false
	}()

	reader := CreateServiceEntryReader(zaptest.NewLogger(t).Sugar(), testClient)

	assert.IsType(t, &informerServiceEntryReader{}, reader)
}

func TestApiServiceReader_Get(t *testing.T) {
	apiReader := CreateServiceReader(zaptest.NewLogger(t).Sugar(), testClient)

	_, err := apiReader.Get("unknown-namespace", "unknown-service")
	assert.ErrorContains(t, err, "not found")

	service, err := apiReader.Get("my-namespace", "my-service")
	assert.NoError(t, err)
	assert.Equal(t, testService.GetObjectMeta(), service.GetObjectMeta())
}

func TestApiServiceReader_List(t *testing.T) {
	apiReader := CreateServiceReader(zaptest.NewLogger(t).Sugar(), testClient)

	services, err := apiReader.List("unknown-namespace", selector)
	assert.NoError(t, err)
	assert.Empty(t, services)

	services, err = apiReader.List("my-namespace", selector)
	assert.NoError(t, err)
	assert.Len(t, services, 1)
	assert.Equal(t, testService.GetObjectMeta(), services[0].GetObjectMeta())
}

func TestApiServiceEntryReader_Get(t *testing.T) {
	apiReader := CreateServiceEntryReader(zaptest.NewLogger(t).Sugar(), testClient)

	_, err := apiReader.Get("unknown-namespace", "unknown-service")
	assert.ErrorContains(t, err, "not found")

	se, err := apiReader.Get("my-namespace", "my-se")
	assert.NoError(t, err)
	assert.Equal(t, testSe.GetObjectMeta(), se.GetObjectMeta())
}

func TestApiServiceEntryReader_List(t *testing.T) {
	apiReader := CreateServiceEntryReader(zaptest.NewLogger(t).Sugar(), testClient)

	list, err := apiReader.List("unknown-namespace", selector)
	assert.NoError(t, err)
	assert.Len(t, list, 0)

	list, err = apiReader.List("my-namespace", selector)
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, testSe.GetObjectMeta(), list[0].GetObjectMeta())
}

func TestInformerServiceReader_Get(t *testing.T) {
	features.UseInformerToEnqueueSvcSe = true
	stopCh := make(chan struct{})
	defer func() {
		features.UseInformerToEnqueueSvcSe = false
		close(stopCh)
	}()
	initInformers(stopCh)

	informerReader := CreateServiceReader(zaptest.NewLogger(t).Sugar(), testClient)

	_, err := informerReader.Get("unknown-namespace", "unknown-service")
	assert.ErrorContains(t, err, "not found")

	service, err := informerReader.Get("my-namespace", "my-service")
	assert.NoError(t, err)
	assert.Equal(t, testService.GetObjectMeta(), service.GetObjectMeta())
}

func TestInformerServiceReader_List(t *testing.T) {
	features.UseInformerToEnqueueSvcSe = true
	stopCh := make(chan struct{})
	defer func() {
		features.UseInformerToEnqueueSvcSe = false
		close(stopCh)
	}()

	initInformers(stopCh)

	informerReader := CreateServiceReader(zaptest.NewLogger(t).Sugar(), testClient)

	services, err := informerReader.List("unknown-namespace", selector)
	assert.NoError(t, err)
	assert.Len(t, services, 0)

	services, err = informerReader.List("my-namespace", selector)
	assert.NoError(t, err)
	assert.Len(t, services, 1)
	assert.Equal(t, testService.GetObjectMeta(), services[0].GetObjectMeta())
}

func TestInformerServiceEntryReader_Get(t *testing.T) {
	features.UseInformerToEnqueueSvcSe = true
	stopCh := make(chan struct{})
	defer func() {
		features.UseInformerToEnqueueSvcSe = false
		close(stopCh)
	}()

	initInformers(stopCh)

	informerReader := CreateServiceEntryReader(zaptest.NewLogger(t).Sugar(), testClient)

	_, err := informerReader.Get("unknown-namespace", "unknown-service")
	assert.ErrorContains(t, err, "not found")

	se, err := informerReader.Get("my-namespace", "my-se")
	assert.NoError(t, err)
	assert.Equal(t, testSe.GetObjectMeta(), se.GetObjectMeta())
}

func TestInformerServiceEntryReader_List(t *testing.T) {
	features.UseInformerToEnqueueSvcSe = true
	stopCh := make(chan struct{})
	defer func() {
		features.UseInformerToEnqueueSvcSe = false
		close(stopCh)
	}()

	initInformers(stopCh)

	informerReader := CreateServiceEntryReader(zaptest.NewLogger(t).Sugar(), testClient)

	list, err := informerReader.List("unknown-namespace", selector)
	assert.NoError(t, err)
	assert.Len(t, list, 0)

	list, err = informerReader.List("my-namespace", selector)
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, testSe.GetObjectMeta(), list[0].GetObjectMeta())
}

func TestRemoteClusterServiceEntryReader(t *testing.T) {
	reader := CreateRemoteClusterServiceEntryReader()

	result, err := reader.Get("some-namespace", "some-servce")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.True(t, kubeError.IsNotFound(err))

	listResult, err := reader.List("some-namespace", selector)
	assert.NoError(t, err)
	assert.NotNil(t, listResult)
	assert.Empty(t, listResult)
}

func initInformers(stopCh chan struct{}) {
	svcInformer := testClient.KubeInformerFactory().Core().V1().Services().Informer()
	seInformer := testClient.IstioInformerFactory().Networking().V1alpha3().ServiceEntries().Informer()
	go svcInformer.Run(stopCh)
	go seInformer.Run(stopCh)
	cache.WaitForCacheSync(stopCh, svcInformer.HasSynced)
	cache.WaitForCacheSync(stopCh, seInformer.HasSynced)
}
