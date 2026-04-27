package controllers

import (
	"fmt"
	"strings"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type AdditionalObjectManager interface {
	CreateInformersForAddObjs(primaryClient kube.Client,
		clusterManager controllers_api.ClusterManager,
		logger *zap.SugaredLogger) error

	AddIndexersToSvcInformer(client kube.Client) error

	GetAdditionalObjectsForExtension(
		mop *meshv1alpha1.MeshOperator,
		svc *unstructured.Unstructured) (map[string]*templating.AdditionalObjects, error)

	GetAdditionalObjectsForService(
		objectType string, svc *corev1.Service) ([]*unstructured.Unstructured, error)

	GetInformers() []informers.GenericInformer
}

type AdditionalObjectManagerImpl struct {
	informers       map[string]informers.GenericInformer
	renderingConfig *RenderingConfig
	metricsRegistry *prometheus.Registry
}

type NoOpAdditionalObjectManager struct {
}

func NewAdditionalObjManager(renderingConfig *RenderingConfig, metricsRegistry *prometheus.Registry) AdditionalObjectManager {
	if renderingConfig == nil {
		return &NoOpAdditionalObjectManager{}
	}
	return &AdditionalObjectManagerImpl{
		informers:       make(map[string]informers.GenericInformer),
		renderingConfig: renderingConfig,
		metricsRegistry: metricsRegistry,
	}
}

func (ac *AdditionalObjectManagerImpl) CreateInformersForAddObjs(primaryClient kube.Client,
	clusterManager controllers_api.ClusterManager,
	logger *zap.SugaredLogger) error {

	for extensionName, extension := range ac.renderingConfig.Extensions {
		for _, additionalObj := range extension.AdditionalObjects {
			resource := schema.GroupVersionResource{
				Group:    additionalObj.Group,
				Version:  additionalObj.Version,
				Resource: additionalObj.Resource,
			}
			resourcePresent, err := isResourcePresentInCluster(resource, primaryClient)
			if err != nil || !resourcePresent {
				logger.Errorf("additional object resource %s for extension %s is not present in cluster: %v", resource.String(), extensionName, err)
				continue
			}

			informerCreateErr := ac.createInformersForAdditionalObj(primaryClient, resource, logger)
			if informerCreateErr != nil {
				return informerCreateErr
			}
			addEventHandlerErr := ac.addAdditionalObjEventHandlerForExtension(additionalObj, resource, clusterManager, logger, extensionName)
			if addEventHandlerErr != nil {
				return addEventHandlerErr
			}
		}
	}

	for _, additionalObject := range ac.renderingConfig.ServiceConfig {
		resource := schema.GroupVersionResource{
			Group:    additionalObject.Group,
			Version:  additionalObject.Version,
			Resource: additionalObject.Resource,
		}

		resourcePresent, err := isResourcePresentInCluster(resource, primaryClient)
		if err != nil || !resourcePresent {
			logger.Errorf("additional object resource %s for extension %s is not present in cluster: %v", resource.String(), err)
			continue
		}

		informerCreateErr := ac.createInformersForAdditionalObj(primaryClient, resource, logger)
		if informerCreateErr != nil {
			return informerCreateErr
		}
		addEventHandlerErr := ac.addAdditionalObjEventHandlerAndIndexerForSvc(additionalObject, resource, clusterManager, logger)
		if addEventHandlerErr != nil {
			return addEventHandlerErr
		}
	}
	return nil
}

func (ac *AdditionalObjectManagerImpl) createInformersForAdditionalObj(primaryClient kube.Client, resource schema.GroupVersionResource, logger *zap.SugaredLogger) error {
	logger.Debugf("create informer for additiona obj of resource type%v", resource.String())
	if ac.informers[resource.String()] == nil {
		informer := primaryClient.DynamicInformerFactory().ForResource(resource)
		errorHandler := NewWatchErrorHandlerWithMetrics(logger, primaryClient.GetClusterName(), "additional-object:"+resource.String(), ac.metricsRegistry)
		_ = informer.Informer().SetWatchErrorHandler(errorHandler)

		ac.informers[resource.String()] = informer
	}

	return nil
}

func (ac *AdditionalObjectManagerImpl) addAdditionalObjEventHandlerForExtension(additionalObj AdditionalObject, resource schema.GroupVersionResource, clusterManager controllers_api.ClusterManager, logger *zap.SugaredLogger, extensionName string) error {
	lookup := additionalObj.Lookup

	if len(lookup.MatchByServiceLabels) > 0 {
		matchLabelNames := lookup.MatchByServiceLabels
		err := addIndexerByLabels(ac.informers[resource.String()].Informer(), matchLabelNames)
		if err != nil {
			return fmt.Errorf("error adding indexer : %s", err.Error())
		}
		addEventHandlerForLabelsLookup(ac.informers[resource.String()], matchLabelNames, additionalObj.Namespace, clusterManager, logger)
	} else if lookup.MatchByName != "" {
		addEventHandlerForNameLookup(ac.informers[resource.String()], extensionName, clusterManager, logger, additionalObj.Namespace, lookup.MatchByName)
	}
	return nil
}

func (ac *AdditionalObjectManagerImpl) addAdditionalObjEventHandlerAndIndexerForSvc(additionalObj AdditionalObject, resource schema.GroupVersionResource, clusterManager controllers_api.ClusterManager, logger *zap.SugaredLogger) error {
	lookup := additionalObj.Lookup

	if lookup.BySvcNameAndNamespace {
		addEventHandlerForByNsAndNameLookup(ac.informers[resource.String()], logger, clusterManager)
	} else if len(lookup.BySvcNameAndNamespaceArray) > 0 {
		addEventHandlerForBySvcNameAndNsArray(ac.informers[resource.String()], logger, lookup.BySvcNameAndNamespaceArray, clusterManager)
		addIndexerBySvcNameAndNsArray(ac.informers[resource.String()].Informer(), logger, lookup.BySvcNameAndNamespaceArray)
	}
	return nil
}

func addIndexerBySvcNameAndNsArray(informer cache.SharedIndexInformer, logger *zap.SugaredLogger, lookup []string) {
	indexName := getAdditionalObjectIndexNameForNameAndNsArray(lookup)

	if indexerExists(informer, indexName) {
		return
	}

	err := informer.AddIndexers(cache.Indexers{
		indexName: func(obj interface{}) (strings []string, e error) {
			item, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return nil, fmt.Errorf("unexpected type for indexer: %T", obj)
			}

			indexingResult := []string{}
			invokeForNamespaceAndNames(logger, item, lookup, func(namespace, name string) {
				indexingResult = append(indexingResult, getAdditionalObjectIndexStringForNameAndNsArray(namespace, name))
			})

			if len(indexingResult) <= 0 {
				return []string{}, nil
			}
			return indexingResult, nil
		},
	})

	if err != nil {
		logger.Errorf("failed to build indexer for additional object: %s", indexName)
	}
}

func addIndexerByLabels(informer cache.SharedIndexInformer, matchLabelNames []string) error {

	indexName := getAdditionalObjectIndexName(matchLabelNames)

	if indexerExists(informer, indexName) {
		return nil
	}

	err := informer.AddIndexers(cache.Indexers{
		indexName: func(obj interface{}) (strings []string, e error) {
			item, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return nil, fmt.Errorf("unexpected type for indexer: %T", obj)
			}

			labelValues := extractLabelValuesOrNil(matchLabelNames, item.GetLabels())
			// skip adding objects missing the labels into index if any label is missing
			if labelValues == nil {
				return []string{}, nil
			}
			return []string{getAdditionalObjectIndexString(item.GetNamespace(), labelValues)}, nil
		},
	})
	return err
}

// getAdditionalObjectIndexName - add-object index name consists of label names only: label1/label2
func getAdditionalObjectIndexName(labelNames []string) string {
	return strings.Join(labelNames, "/")
}

// getAdditionalObjectIndexString - add-object index string consists of namespace and label values: my-namespace/value1/value2
func getAdditionalObjectIndexString(namespace string, labelValues []string) string {
	return namespace + "/" + strings.Join(labelValues, "/")
}

func getAdditionalObjectIndexNameForNameAndNsArray(lookup []string) string {
	return strings.Join(lookup, "/")
}

func getAdditionalObjectIndexStringForNameAndNsArray(namespace, name string) string {
	return namespace + "/" + name
}

// getServiceObjectIndexString - service index string consists only of the label values: value1/value2
func getServiceObjectIndexString(labelValues []string) string {
	return strings.Join(labelValues, "/")
}

func indexerExists(informer cache.SharedIndexInformer, indexName string) bool {
	indexers := informer.GetIndexer().GetIndexers()
	_, exists := indexers[indexName]
	return exists
}

func addEventHandlerForLabelsLookup(informer informers.GenericInformer,
	matchLabelNames []string,
	additionalObjectsNamespace string,
	clusterStore controllers_api.ClusterManager,
	logger *zap.SugaredLogger) {

	informer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			// Only consider events from additional-objects that happen in the namespace configured in rendering-config.
			item := obj.(*unstructured.Unstructured)
			return item.GetNamespace() == additionalObjectsNamespace
		},
		Handler: createEventHandlerForLabelsLookup(matchLabelNames, clusterStore, logger),
	})
}

func addEventHandlerForByNsAndNameLookup(informer informers.GenericInformer, logger *zap.SugaredLogger, clusterStore controllers_api.ClusterManager) {
	informer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			return true
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				item := obj.(*unstructured.Unstructured)
				logger.Debugw("informer event: Add", "namespace", item.GetNamespace(), "objName", item.GetName())
				enqueueServiceFromAdditionalObject(item, clusterStore, controllers_api.EventUpdate)
			},
			UpdateFunc: func(old, new interface{}) {
				newObj := new.(*unstructured.Unstructured)
				oldObj := old.(*unstructured.Unstructured)
				if oldObj.GetResourceVersion() == newObj.GetResourceVersion() {
					return
				}
				if oldObj.GetGeneration() == newObj.GetGeneration() && !labelsChanged(oldObj, newObj) {
					return
				}
				logger.Debugw("informer event: Update", "namespace", oldObj.GetNamespace(), "objName", oldObj.GetName(),
					"newObjRV", oldObj.GetResourceVersion(), "oldObjRV", newObj.GetResourceVersion())
				enqueueServiceFromAdditionalObject(oldObj, clusterStore, controllers_api.EventUpdate)
				enqueueServiceFromAdditionalObject(newObj, clusterStore, controllers_api.EventUpdate)
			},
			DeleteFunc: func(obj interface{}) {
				item := obj.(*unstructured.Unstructured)
				logger.Debugw("informer event: Delete", "namespace", item.GetNamespace(), "svcName", item.GetName())
				enqueueServiceFromAdditionalObject(item, clusterStore, controllers_api.EventUpdate)
			},
		},
	})
}

func addEventHandlerForBySvcNameAndNsArray(informer informers.GenericInformer, logger *zap.SugaredLogger, lookup []string, clusterStore controllers_api.ClusterManager) {
	informer.Informer().AddEventHandler(createEventHandlerForBySvcNsArray(logger, lookup, clusterStore))
}

func createEventHandlerForBySvcNsArray(logger *zap.SugaredLogger, lookup []string, clusterStore controllers_api.ClusterManager) cache.FilteringResourceEventHandler {
	return cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			return true
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				item := obj.(*unstructured.Unstructured)
				logger.Debugw("informer event: Add", "namespace", item.GetNamespace(), "objName", item.GetName())
				enqueueServicesForNameAndNamespaceArray(logger, lookup, item, clusterStore)
			},
			UpdateFunc: func(old, new interface{}) {
				newObj := new.(*unstructured.Unstructured)
				oldObj := old.(*unstructured.Unstructured)
				if oldObj.GetResourceVersion() == newObj.GetResourceVersion() {
					return
				}
				if oldObj.GetGeneration() == newObj.GetGeneration() && !labelsChanged(oldObj, newObj) {
					return
				}
				logger.Debugw("informer event: Update", "namespace", oldObj.GetNamespace(), "objName", oldObj.GetName(),
					"newObjRV", oldObj.GetResourceVersion(), "oldObjRV", newObj.GetResourceVersion())
				enqueueServicesForNameAndNamespaceArray(logger, lookup, newObj, clusterStore)
				enqueueServicesForNameAndNamespaceArray(logger, lookup, oldObj, clusterStore)
			},
			DeleteFunc: func(obj interface{}) {
				item := obj.(*unstructured.Unstructured)
				logger.Debugw("informer event: Delete", "namespace", item.GetNamespace(), "svcName", item.GetName())
				enqueueServicesForNameAndNamespaceArray(logger, lookup, item, clusterStore)
			},
		},
	}
}

func createEventHandlerForLabelsLookup(matchLabelNames []string,
	clusterStore controllers_api.ClusterManager,
	logger *zap.SugaredLogger) cache.ResourceEventHandlerFuncs {

	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			item := obj.(*unstructured.Unstructured)
			logger.Debugw("informer event: Add", "namespace", item.GetNamespace(), "objName", item.GetName())
			enqueueServiceMopsFromAdditionalObject(item, matchLabelNames, clusterStore, logger)

		},
		UpdateFunc: func(old, new interface{}) {
			newObj := new.(*unstructured.Unstructured)
			oldObj := old.(*unstructured.Unstructured)
			if oldObj.GetResourceVersion() == newObj.GetResourceVersion() {
				return
			}
			if oldObj.GetGeneration() == newObj.GetGeneration() && !labelsChanged(oldObj, newObj) {
				return
			}
			logger.Debugw("informer event: Update", "namespace", oldObj.GetNamespace(), "objName", oldObj.GetName(),
				"newObjRV", oldObj.GetResourceVersion(), "oldObjRV", newObj.GetResourceVersion())
			enqueueServiceMopsFromAdditionalObject(oldObj, matchLabelNames, clusterStore, logger)
			enqueueServiceMopsFromAdditionalObject(newObj, matchLabelNames, clusterStore, logger)
		},
		DeleteFunc: func(obj interface{}) {
			item := obj.(*unstructured.Unstructured)
			logger.Debugw("informer event: Delete", "namespace", item.GetNamespace(), "svcName", item.GetName())
			enqueueServiceMopsFromAdditionalObject(item, matchLabelNames, clusterStore, logger)

		},
	}
}

func enqueueServicesForNameAndNamespaceArray(logger *zap.SugaredLogger, lookup []string, obj *unstructured.Unstructured, clusterStore controllers_api.ClusterManager) {
	invokeForNamespaceAndNames(logger, obj, lookup, func(namespace, name string) {
		for _, cluster := range clusterStore.GetExistingClusters() {
			lister := cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Lister()
			service, err := lister.Services(namespace).Get(name)
			if err != nil && !k8serrors.IsNotFound(err) {
				logger.Errorf("failed to fetch service for additional object %s/%s: %s", namespace, name, err)
				continue
			}
			if service != nil {
				logger.Infof("enqueued service %s/%s for additional object: %s/%s", namespace, name, obj.GetNamespace(), obj.GetName())
				cluster.GetMulticlusterServiceController().GetObjectEnqueuer().Enqueue(string(cluster.GetId()), service, controllers_api.EventUpdate)
			}
		}
	})
}

func invokeForNamespaceAndNames(logger *zap.SugaredLogger, obj *unstructured.Unstructured, lookup []string, callback func(namespace, name string)) {
	ctxLogger := logger.With("ad_obj_namespace", obj.GetNamespace()).
		With("add_obj_name", obj.GetName()).
		With("lookup", lookup)

	nameAndNamespaceArray, found, err := unstructured.NestedSlice(obj.Object, lookup...)
	if err != nil {
		ctxLogger.Errorf("failed to parse lookup for additional object")
		return
	}
	if !found {
		return
	}

	for i := range nameAndNamespaceArray {
		nameAndNs, ok := nameAndNamespaceArray[i].(map[string]interface{})
		if !ok {
			ctxLogger.Errorf("object has invalid format")
			continue
		}
		namespace, namespaceFound, _ := unstructured.NestedString(nameAndNs, "namespace")
		name, nameFound, _ := unstructured.NestedString(nameAndNs, "name")
		if !namespaceFound || !nameFound {
			ctxLogger.Errorf("object doesn't have enough information to enqueue: %s", nameAndNs)
			continue
		}
		if obj.GetNamespace() != namespace {
			ctxLogger.Errorf("additional objects are only allowed to target the same-namespace services")
			continue
		}

		callback(namespace, name)
	}
}

func enqueueServiceMopsFromAdditionalObject(
	obj *unstructured.Unstructured,
	matchLabelNames []string,
	clusterStore controllers_api.ClusterManager,
	logger *zap.SugaredLogger) {

	svcIndexName := getAdditionalObjectIndexName(matchLabelNames)

	labelValues := extractLabelValuesOrNil(matchLabelNames, obj.GetLabels())
	if labelValues == nil {
		// short-cirquit. object is missing match labels
		return
	}
	svcIndexValue := getServiceObjectIndexString(labelValues)

	// We need to requeue MOPs for service in every cluster
	for _, cluster := range clusterStore.GetExistingClusters() {
		svcInformer := cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Informer()
		services, err := svcInformer.GetIndexer().ByIndex(svcIndexName, svcIndexValue)
		if err != nil {
			logger.Errorf("error finding services for cluster: %s, index: %s, value: %s, %v", cluster.GetId(), svcIndexName, svcIndexValue, err.Error())
			return
		}

		for _, service := range services {
			if svc := service.(*corev1.Service); svc != nil {
				EnqueueMopReferencingGivenService(logger, cluster.GetId().String(), cluster.GetKubeClient(), cluster.GetMopEnqueuer(), svc)
			}
		}
	}
}

func enqueueServiceFromAdditionalObject(obj *unstructured.Unstructured, clusterStore controllers_api.ClusterManager, event controllers_api.Event) {
	for _, cluster := range clusterStore.GetExistingClusters() {
		cluster.GetMulticlusterServiceController().GetObjectEnqueuer().Enqueue(string(cluster.GetId()), obj, event)
	}
}

func addEventHandlerForNameLookup(informer informers.GenericInformer,
	extensionName string,
	clusterStore controllers_api.ClusterManager,
	logger *zap.SugaredLogger,
	additionalObjectsNamespace string,
	additionalObjectName string) {

	informer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: eventHandlerByNameFilter(additionalObjectsNamespace, additionalObjectName),
		Handler:    createEventHandlerForNameLookup(extensionName, clusterStore, logger),
	})
}

func eventHandlerByNameFilter(additionalObjectsNamespace string, additionalObjectName string) func(obj interface{}) bool {
	return func(obj interface{}) bool {
		item := obj.(*unstructured.Unstructured)
		return item.GetNamespace() == additionalObjectsNamespace &&
			item.GetName() == additionalObjectName
	}
}

func createEventHandlerForNameLookup(extensionName string,
	clusterStore controllers_api.ClusterManager,
	logger *zap.SugaredLogger) cache.ResourceEventHandlerFuncs {

	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			item := obj.(*unstructured.Unstructured)
			logger.Debugw("informer event: Add", "namespace", item.GetNamespace(), "objName", item.GetName())
			enqueueMopsByExtensionName(extensionName, clusterStore, logger)

		},
		UpdateFunc: func(old, new interface{}) {
			newObj := new.(*unstructured.Unstructured)
			oldObj := old.(*unstructured.Unstructured)
			logger.Debugw("informer event: Update", "namespace", oldObj.GetNamespace(), "objName", oldObj.GetName(),
				"newObjRV", newObj.GetResourceVersion(), "oldObjRV", oldObj.GetResourceVersion(), "newObjGeneration", newObj.GetGeneration(), "oldObjGeneration", oldObj.GetGeneration())
			if newObj.GetResourceVersion() == oldObj.GetResourceVersion() {
				return
			}
			if oldObj.GetGeneration() == newObj.GetGeneration() &&
				oldObj.GetGeneration() > 0 && newObj.GetGeneration() > 0 &&
				!labelsChanged(oldObj, newObj) {
				return
			}
			enqueueMopsByExtensionName(extensionName, clusterStore, logger)
		},
		DeleteFunc: func(obj interface{}) {
			item := obj.(*unstructured.Unstructured)
			logger.Debugw("informer event: Delete", "namespace", item.GetNamespace(), "objName", item.GetName())
			enqueueMopsByExtensionName(extensionName, clusterStore, logger)
		},
	}
}

func enqueueMopsByExtensionName(
	extensionName string,
	clusterStore controllers_api.ClusterManager,
	logger *zap.SugaredLogger) {

	for _, cluster := range clusterStore.GetExistingClusters() {

		mopInformer := cluster.GetKubeClient().MopInformerFactory().Mesh().V1alpha1().MeshOperators().Informer()

		mops, err := mopInformer.GetIndexer().ByIndex(constants.ExtensionIndexName, extensionName)
		if err != nil {
			logger.Errorf("error finding mops for cluster: %s, index: %s, value: %s, %v", cluster.GetId(), constants.ExtensionIndexName, extensionName, err.Error())
			return
		}

		for _, mop := range mops {
			cluster.GetMopEnqueuer().Enqueue(cluster.GetId().String(), mop, controllers_api.EventUpdate)
		}
	}
}

// extractLabelValuesOrNil - returns a list of the label values or nil if any of the labels is missing
func extractLabelValuesOrNil(labelNames []string, labels map[string]string) []string {
	var labelValues []string
	for _, labelKey := range labelNames {
		labelValue, keyExists := labels[labelKey]
		if !keyExists {
			return nil
		}
		labelValues = append(labelValues, labelValue)
	}
	return labelValues
}

func (ac *AdditionalObjectManagerImpl) AddIndexersToSvcInformer(client kube.Client) error {

	informer := client.KubeInformerFactory().Core().V1().Services().Informer()

	for _, extension := range ac.renderingConfig.Extensions {
		for _, additionalObj := range extension.AdditionalObjects {
			labelNames := additionalObj.Lookup.MatchByServiceLabels

			indexName := getAdditionalObjectIndexName(labelNames)

			if indexerExists(informer, indexName) {
				continue
			}

			err := informer.AddIndexers(cache.Indexers{
				indexName: func(obj interface{}) (strings []string, e error) {
					svc := obj.(*corev1.Service)
					if svc == nil {
						return nil, e
					}
					labelValues := extractLabelValuesOrNil(labelNames, svc.GetLabels())
					if labelValues == nil {
						// don't index objects if it is missing match labels
						return []string{}, nil
					}
					svcIndexString := getServiceObjectIndexString(labelValues)
					return []string{svcIndexString}, nil
				},
			})

			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ac *AdditionalObjectManagerImpl) GetAdditionalObjectsForService(objectType string, svc *corev1.Service) ([]*unstructured.Unstructured, error) {
	additionalObj := ac.renderingConfig.ServiceConfig[objectType]

	var objects []interface{}
	var err error
	if additionalObj.Lookup.BySvcNameAndNamespace {
		objects, err = ac.fetchAdditionalObjsByName(additionalObj, svc.GetNamespace(), svc.GetName())
	} else if len(additionalObj.Lookup.BySvcNameAndNamespaceArray) > 0 {
		objects, err = ac.fetchAdditionalObjectsByNamespaceAndNameArray(additionalObj, svc)
	}

	if err != nil {
		return nil, err
	}

	var unstructuredObjs []*unstructured.Unstructured
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		unstructuredObjs = append(unstructuredObjs, obj.(*unstructured.Unstructured))
	}

	return unstructuredObjs, nil
}

func (ac *AdditionalObjectManagerImpl) GetAdditionalObjectsForExtension(mop *meshv1alpha1.MeshOperator, svc *unstructured.Unstructured) (map[string]*templating.AdditionalObjects, error) {

	var templateAdditionalObjects = make(map[string]*templating.AdditionalObjects)

	for _, ext := range mop.Spec.Extensions {
		extensionType, err := templating.GetExtensionType(ext)
		if err != nil {
			return nil, fmt.Errorf("failed to determine extension type: %w", err)
		}

		extensionConfig, extensionConfigExists := ac.renderingConfig.Extensions[extensionType]

		if !extensionConfigExists {
			continue
		}

		additionalObjectsConfigs := extensionConfig.AdditionalObjects

		for _, additionalObjectConfig := range additionalObjectsConfigs {

			var objects []interface{}

			lookup := additionalObjectConfig.Lookup

			if len(lookup.MatchByServiceLabels) > 0 {
				objects, err = ac.fetchAdditionalObjsBySvcLabels(additionalObjectConfig, svc)
			} else if lookup.MatchByName != "" {
				objects, err = ac.fetchAdditionalObjsByName(additionalObjectConfig, additionalObjectConfig.Namespace, additionalObjectConfig.Lookup.MatchByName)
			}
			if err != nil {
				return nil, err
			}

			if len(objects) > 0 {
				if additionalObjectConfig.Singleton && len(objects) > 1 { // only one object expected.

					return nil, fmt.Errorf("more than one additional object found")

				}
				if templateAdditionalObjects[extensionType] == nil {
					templateAdditionalObjects[extensionType] = &templating.AdditionalObjects{
						Singletons: make(map[string]interface{}),
					}
				}
				templateAdditionalObjects[extensionType].Singletons[additionalObjectConfig.ContextKey] = objects[0]
			}
		}
	}
	return templateAdditionalObjects, nil
}

func (ac *AdditionalObjectManagerImpl) fetchAdditionalObjsBySvcLabels(additionalObj AdditionalObject, svc *unstructured.Unstructured) ([]interface{}, error) {

	resource := schema.GroupVersionResource{
		Group:    additionalObj.Group,
		Version:  additionalObj.Version,
		Resource: additionalObj.Resource,
	}

	labelNames := additionalObj.Lookup.MatchByServiceLabels
	labelValues := extractLabelValuesOrNil(labelNames, svc.GetLabels())
	if labelValues == nil {
		// short-cirquit. object is missing match labels. return early
		return nil, fmt.Errorf("%s is missing labels to lookup an additional object: %s", strings.ToLower(svc.GetKind()), labelNames)
	}

	additionalObjectIndexName := getAdditionalObjectIndexName(labelNames)
	additionalObjectIndexString := getAdditionalObjectIndexString(additionalObj.Namespace, labelValues)

	addObjInformer, exists := ac.informers[resource.String()]
	if !exists {
		return nil, fmt.Errorf("additional object informer doesn't exist for resource: %s", resource.String())
	}

	objects, err := addObjInformer.Informer().GetIndexer().ByIndex(additionalObjectIndexName, additionalObjectIndexString)
	if err != nil {
		return nil, fmt.Errorf("error fetching additionalObjects : %w", err)
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("additional object: %s is missing", additionalObjectIndexString)
	}
	return objects, nil
}

func (ac *AdditionalObjectManagerImpl) fetchAdditionalObjsByName(additionalObj AdditionalObject, namespace, name string) ([]interface{}, error) {

	resource := schema.GroupVersionResource{
		Group:    additionalObj.Group,
		Version:  additionalObj.Version,
		Resource: additionalObj.Resource,
	}

	addObjInformer, exists := ac.informers[resource.String()]
	if !exists {
		return nil, fmt.Errorf("additional object informer doesn't exist for resource: %s", resource.String())
	}

	object, err := addObjInformer.Lister().ByNamespace(namespace).Get(name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("additional object: %s/%s is missing", namespace, name)
		}
		return nil, fmt.Errorf("error fetching additionalObjects : %w", err)
	}
	return []interface{}{object}, nil
}

func (ac *AdditionalObjectManagerImpl) fetchAdditionalObjectsByNamespaceAndNameArray(additionalObj AdditionalObject, svc *corev1.Service) ([]interface{}, error) {
	resource := schema.GroupVersionResource{
		Group:    additionalObj.Group,
		Version:  additionalObj.Version,
		Resource: additionalObj.Resource,
	}

	addObjInformer, exists := ac.informers[resource.String()]
	if !exists {
		return nil, fmt.Errorf("additional object informer doesn't exist for resource: %s", resource.String())
	}
	indexName := getAdditionalObjectIndexNameForNameAndNsArray(additionalObj.Lookup.BySvcNameAndNamespaceArray)
	indexString := getAdditionalObjectIndexStringForNameAndNsArray(svc.GetNamespace(), svc.GetName())

	objects, err := addObjInformer.Informer().GetIndexer().ByIndex(indexName, indexString)
	if err != nil {
		return nil, fmt.Errorf("error fetching additionalObjects : %w", err)
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("additional object: %s is missing", indexString)
	}
	return objects, nil
}

func (ac *AdditionalObjectManagerImpl) GetInformers() []informers.GenericInformer {
	var result []informers.GenericInformer
	for _, informer := range ac.informers {
		result = append(result, informer)
	}

	return result
}

func isResourcePresentInCluster(resource schema.GroupVersionResource, client kube.Client) (bool, error) {
	resources, err := client.Discovery().ServerResourcesForGroupVersion(resource.GroupVersion().String())
	if err != nil && k8serrors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to resolve resources for additional object config %s, %w", resource.String(), err)
	}

	for _, apiResource := range resources.APIResources {
		if apiResource.Name == resource.Resource {
			return true, nil
		}
	}
	return false, nil
}

func (no *NoOpAdditionalObjectManager) CreateInformersForAddObjs(_ kube.Client,
	_ controllers_api.ClusterManager,
	_ *zap.SugaredLogger) error {

	return nil
}

func (no *NoOpAdditionalObjectManager) AddIndexersToSvcInformer(_ kube.Client) error {
	return nil
}

func (no *NoOpAdditionalObjectManager) GetInformers() []informers.GenericInformer {
	return []informers.GenericInformer{}
}

func (no *NoOpAdditionalObjectManager) GetAdditionalObjectsForExtension(_ *meshv1alpha1.MeshOperator, _ *unstructured.Unstructured) (
	map[string]*templating.AdditionalObjects, error) {
	return map[string]*templating.AdditionalObjects{}, nil
}

func (no *NoOpAdditionalObjectManager) GetAdditionalObjectsForService(_ string, _ *corev1.Service) ([]*unstructured.Unstructured, error) {
	return nil, nil
}
