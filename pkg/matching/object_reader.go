package matching

import (
	"context"
	"net/http"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"go.uber.org/zap"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"

	istioinformersv1alpha3 "istio.io/client-go/pkg/informers/externalversions/networking/v1alpha3"
	corev1informers "k8s.io/client-go/informers/core/v1"

	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	networkingv1alpha3 "istio.io/client-go/pkg/clientset/versioned/typed/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

func CreateServiceReader(logger *zap.SugaredLogger, client kube.Client) ServiceReader {
	if features.UseInformerToEnqueueSvcSe {
		logger.Info("using informer reader to enqueue services")
		return &informerServiceReader{
			svcInformer: client.KubeInformerFactory().Core().V1().Services(),
		}
	}

	logger.Info("using api reader to enqueue services")
	return &apiServiceReader{
		apiGetter: client.Kube().CoreV1(),
	}
}

func CreateServiceEntryReader(logger *zap.SugaredLogger, client kube.Client) ServiceEntryReader {
	if features.UseInformerToEnqueueSvcSe {
		logger.Info("using informer reader to enqueue service-entries")
		return &informerServiceEntryReader{
			seInformer: client.IstioInformerFactory().Networking().V1alpha3().ServiceEntries(),
		}
	}

	logger.Info("using api reader to enqueue service-entries")
	return &apiServiceEntryReader{
		apiGetter: client.Istio().NetworkingV1alpha3(),
	}
}

func CreateRemoteClusterServiceEntryReader() ServiceEntryReader {
	return &remoteClusterServiceEntryReader{}
}

type ServiceReader interface {
	Get(namespace, name string) (*v1.Service, error)
	List(namespace string, selector labels.Selector) ([]*v1.Service, error)
}

type ServiceEntryReader interface {
	Get(namespace, name string) (*istiov1alpha3.ServiceEntry, error)
	List(namespace string, selector labels.Selector) ([]*istiov1alpha3.ServiceEntry, error)
}

// remoteClusterServiceEntryReader - service entries have no effect in remote clusters. This reader returns empty results.
type remoteClusterServiceEntryReader struct{}

func (rcser *remoteClusterServiceEntryReader) Get(_, _ string) (*istiov1alpha3.ServiceEntry, error) {
	return nil, &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound}}
}

func (rcser *remoteClusterServiceEntryReader) List(_ string, _ labels.Selector) ([]*istiov1alpha3.ServiceEntry, error) {
	return []*istiov1alpha3.ServiceEntry{}, nil
}

// apiServiceReader - non cached, makes direct api calls to obtain Service objects
type apiServiceReader struct {
	apiGetter corev1.CoreV1Interface
}

func (sr *apiServiceReader) Get(namespace, name string) (*v1.Service, error) {
	return sr.apiGetter.Services(namespace).Get(context.Background(), name, metav1.GetOptions{})
}

func (sr *apiServiceReader) List(namespace string, selector labels.Selector) ([]*v1.Service, error) {
	servicesList, err := sr.apiGetter.Services(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector.String()})

	if err != nil {
		return nil, err
	}

	result := make([]*v1.Service, len(servicesList.Items))
	for i := range servicesList.Items {
		result[i] = &servicesList.Items[i]
	}
	return result, nil
}

// apiServiceEntryReader - non cached, makes direct api calls to obtain ServiceEntry objects
type apiServiceEntryReader struct {
	apiGetter networkingv1alpha3.NetworkingV1alpha3Interface
}

func (ser *apiServiceEntryReader) Get(namespace, name string) (*istiov1alpha3.ServiceEntry, error) {
	return ser.apiGetter.ServiceEntries(namespace).Get(context.Background(), name, metav1.GetOptions{})
}

func (ser *apiServiceEntryReader) List(namespace string, selector labels.Selector) ([]*istiov1alpha3.ServiceEntry, error) {
	serviceEntriesList, err := ser.apiGetter.ServiceEntries(namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}

	return serviceEntriesList.Items, nil
}

// informerServiceReader - cached, uses informer to obtain Service objects
type informerServiceReader struct {
	svcInformer corev1informers.ServiceInformer
}

func (isr *informerServiceReader) Get(namespace, name string) (*v1.Service, error) {
	return isr.svcInformer.Lister().Services(namespace).Get(name)
}

func (isr *informerServiceReader) List(namespace string, selector labels.Selector) ([]*v1.Service, error) {
	list, err := isr.svcInformer.Lister().Services(namespace).List(selector)
	if err != nil {
		return nil, err
	}
	return list, nil
}

// informerServiceEntryReader - cached, uses informer to obtain ServiceEntry objects
type informerServiceEntryReader struct {
	seInformer istioinformersv1alpha3.ServiceEntryInformer
}

func (iser *informerServiceEntryReader) Get(namespace, name string) (*istiov1alpha3.ServiceEntry, error) {
	return iser.seInformer.Lister().ServiceEntries(namespace).Get(name)
}

func (iser *informerServiceEntryReader) List(namespace string, selector labels.Selector) ([]*istiov1alpha3.ServiceEntry, error) {
	list, err := iser.seInformer.Lister().ServiceEntries(namespace).List(selector)
	if err != nil {
		return nil, err
	}
	return list, nil
}
