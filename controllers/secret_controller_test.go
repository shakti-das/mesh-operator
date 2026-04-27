package controllers

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"
	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"go.uber.org/zap/zapcore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	kubeError "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	SecretWatchNamespace = "secret-ns"
)

func TestSecretReconcileTracker_initialQueueItemsReconciled(t *testing.T) {
	testCases := []struct {
		name             string
		initialQueueSize int
		itemsReconciled  int
		expectedResult   bool
	}{
		{
			name:             "no secrets",
			initialQueueSize: 0,
			itemsReconciled:  0,
			expectedResult:   true,
		},
		{
			name:             "all secrets processed",
			initialQueueSize: 1,
			itemsReconciled:  1,
			expectedResult:   true,
		},
		{
			name:             "not fully processed",
			initialQueueSize: 2,
			itemsReconciled:  1,
			expectedResult:   false,
		},
		{
			name:             "fully processed and secret updated",
			initialQueueSize: 2,
			itemsReconciled:  3,
			expectedResult:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := NewSecretReconcileTracker()

			// Set initial size
			tracker.setInitialQueueSize(tc.initialQueueSize)

			// Track items
			for i := 0; i < tc.itemsReconciled; i++ {
				tracker.trackItemReconcile()
			}

			assert.Equal(t, tc.expectedResult, tracker.initialQueueItemsReconciled())
		})
	}
}

func TestReconcileSecrets(t *testing.T) {
	clusterName := "primary"
	testCases := []struct {
		name                  string
		queueItem             QueueItem
		readFromClusterError  error
		objectReadFromCluster *corev1.Secret
		upsertActionExpected  bool
		deleteActionExpected  bool
		expectedReconcileErr  error
	}{
		{
			name: "UpdateSecret",
			queueItem: NewQueueItem(
				clusterName,
				"test/s1",
				"uid-1",
				controllers_api.EventUpdate),
			upsertActionExpected:  true,
			objectReadFromCluster: kube_test.CreateSecret("s1", SecretWatchNamespace, kube_test.ClusterCredential{ClusterID: "c1", Kubeconfig: []byte("kubeconfig-update-c1")}),
		},
		{
			name: "DeleteSecret",
			queueItem: NewQueueItem(
				clusterName,
				"test/s1",
				"uid-1",
				controllers_api.EventDelete),
			deleteActionExpected: true,
		},
		{
			name: "ObjectNotFound",
			queueItem: NewQueueItem(
				clusterName,
				"test/s1",
				"uid-1",
				controllers_api.EventDelete),
			deleteActionExpected: true,
			readFromClusterError: kubeError.NewNotFound(schema.GroupResource{Group: "", Resource: "Secrets"}, "s1"),
		},
		{
			name: "ErrorReadingObject",
			queueItem: NewQueueItem(
				clusterName,
				"test/s1",
				"uid-1",
				controllers_api.EventDelete),
			readFromClusterError: errors.New("random error"),
			expectedReconcileErr: errors.New("random error"),
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			var upsertCalledForObject interface{}
			var isUpsertHandlerInvoked bool
			var isDeleteHandlerInvoked bool

			var objectReader = func(string, string) (interface{}, error) {
				if tc.readFromClusterError != nil {
					return nil, tc.readFromClusterError
				}
				return tc.objectReadFromCluster, nil
			}
			var upsertSecretHandler = func(secretKey string, s *corev1.Secret, event controllers_api.Event) {
				isUpsertHandlerInvoked = true
				upsertCalledForObject = s
			}

			var deleteSecretHandler = func(secretKey string) {
				isDeleteHandlerInvoked = true
			}

			secretController := &secretController{logger: logger, secretReconcileTracker: NewSecretReconcileTracker()}
			secretController.objectReader = objectReader
			secretController.upsertSecretHandler = upsertSecretHandler
			secretController.deleteSecretHandler = deleteSecretHandler

			err := secretController.reconcile(tc.queueItem)

			assert.Nil(t, err)

			if tc.upsertActionExpected {
				assert.Equal(t, true, isUpsertHandlerInvoked)
				assert.Equal(t, tc.objectReadFromCluster, upsertCalledForObject)
			} else if tc.deleteActionExpected {
				assert.Equal(t, true, isDeleteHandlerInvoked)
			} else {
				assert.Equal(t, false, isUpsertHandlerInvoked)
				assert.Equal(t, false, isDeleteHandlerInvoked)
			}
		})
	}
}

func TestReconcileTracksItemForAllPaths(t *testing.T) {
	clusterName := "primary"
	logger := zaptest.NewLogger(t).Sugar()

	testCases := []struct {
		name                  string
		queueItem             []QueueItem
		objectReadFromCluster *corev1.Secret
		initialQueueSize      int
		reconcileCount        int
		expectedInitialized   bool
	}{
		{
			name: "delete path counts toward initialization",
			queueItem: []QueueItem{NewQueueItem(
				clusterName,
				"test/s1",
				"uid-1",
				controllers_api.EventUpdate)},
			objectReadFromCluster: nil, // nil object → handleDelete path
			initialQueueSize:      1,
			reconcileCount:        1,
			expectedInitialized:   true,
		},
		{
			name: "mixed delete and upsert paths reach initialization threshold",
			queueItem: []QueueItem{
				NewQueueItem(
					clusterName,
					"test/s1",
					"uid-1",
					controllers_api.EventUpdate),
				NewQueueItem(
					clusterName,
					"test/s1",
					"uid-1",
					controllers_api.EventUpdate),
				NewQueueItem(
					clusterName,
					"test/s1",
					"uid-1",
					controllers_api.EventDelete)},
			objectReadFromCluster: nil,
			initialQueueSize:      3,
			reconcileCount:        3,
			expectedInitialized:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &secretController{
				logger:                 logger,
				secretReconcileTracker: NewSecretReconcileTracker(),
			}
			sc.objectReader = func(string, string) (interface{}, error) {
				return tc.objectReadFromCluster, nil
			}
			sc.upsertSecretHandler = func(secretKey string, s *corev1.Secret, event controllers_api.Event) {}
			sc.deleteSecretHandler = func(secretKey string) {}
			sc.secretReconcileTracker.setInitialQueueSize(tc.initialQueueSize)

			for i := 0; i < len(tc.queueItem); i++ {
				_ = sc.reconcile(tc.queueItem[i])
			}

			assert.Equal(t, tc.expectedInitialized, sc.allClustersInitialized(),
				"allClustersInitialized should be %v after %d reconciles (initialQueueSize=%d)",
				tc.expectedInitialized, tc.reconcileCount, tc.initialQueueSize)
		})
	}
}

func TestIsMultiClusterLabeledSecret(t *testing.T) {
	testCases := []struct {
		name           string
		namespaceName  string
		labels         map[string]string
		expectedResult bool
	}{
		{
			name:           "MultiClusterLabeledSecretInMcpNamespace",
			namespaceName:  SecretWatchNamespace,
			labels:         map[string]string{constants.MultiClusterSecretLabel: "true"},
			expectedResult: true,
		},
		{
			name:           "MultiClusterLabeledOtherNamespace",
			namespaceName:  "whatever",
			labels:         map[string]string{constants.MultiClusterSecretLabel: "true"},
			expectedResult: false,
		},
		{
			name:           "NonMultiClusterLabeledSecretOtherNamespace",
			namespaceName:  "whatever",
			labels:         map[string]string{"test": "true"},
			expectedResult: false,
		},
		{
			name:           "NonMultiClusterLabeledSecretInMcpNamespace",
			namespaceName:  SecretWatchNamespace,
			labels:         map[string]string{"test": "true"},
			expectedResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: tc.namespaceName,
					Labels:    tc.labels,
				},
			}
			sc := secretController{namespace: SecretWatchNamespace}
			assert.Equal(t, tc.expectedResult, sc.IsMultiClusterLabeledSecret(secret))
		})
	}
}

func TestHandleUpsert(t *testing.T) {
	primaryClusterName := "primary-cluster"
	testCases := []struct {
		name                         string
		secretKey                    string
		secret                       *corev1.Secret
		event                        controllers_api.Event
		remoteClusterCreationError   bool
		failingClusterID             string
		expectedAddedClusterIDList   []cluster.ID
		expectedDeletedClusterIDList []cluster.ID
	}{
		{
			name:                         "AddSecret",
			secretKey:                    SecretWatchNamespace + "/" + "secret1",
			secret:                       kube_test.CreateSecret("secrets1", SecretWatchNamespace, kube_test.ClusterCredential{ClusterID: "c1", Kubeconfig: []byte("kubeconfig-c1")}, kube_test.ClusterCredential{ClusterID: "c2", Kubeconfig: []byte("kubeconfig-c2")}, kube_test.ClusterCredential{ClusterID: primaryClusterName, Kubeconfig: []byte("kubeconfig-primary")}),
			event:                        controllers_api.EventAdd,
			expectedAddedClusterIDList:   []cluster.ID{"c1"},
			expectedDeletedClusterIDList: nil,
		},
		{
			name:                         "UpdateSecret",
			secretKey:                    SecretWatchNamespace + "/" + "secret2",
			secret:                       kube_test.CreateSecret("secrets2", SecretWatchNamespace, kube_test.ClusterCredential{ClusterID: "c2", Kubeconfig: []byte("kubeconfig-c2-updated")}, kube_test.ClusterCredential{ClusterID: primaryClusterName, Kubeconfig: []byte("kubeconfig-primary")}),
			event:                        controllers_api.EventAdd,
			expectedAddedClusterIDList:   []cluster.ID{"c2"},
			expectedDeletedClusterIDList: []cluster.ID{"c2", "c3"},
		},
		{
			name:                         "ErrorAddingRemoteCluster",
			secretKey:                    SecretWatchNamespace + "/" + "secretX",
			secret:                       kube_test.CreateSecret("secretsX", SecretWatchNamespace, kube_test.ClusterCredential{ClusterID: "cX", Kubeconfig: []byte("kubeconfig-cX")}, kube_test.ClusterCredential{ClusterID: primaryClusterName, Kubeconfig: []byte("kubeconfig-primary")}),
			event:                        controllers_api.EventAdd,
			remoteClusterCreationError:   true,
			expectedAddedClusterIDList:   nil,
			expectedDeletedClusterIDList: []cluster.ID{},
		},
		{
			name:      "PartialRemoteClusterFailure",
			secretKey: SecretWatchNamespace + "/" + "secretPartial",
			secret: kube_test.CreateSecret("secretsPartial", SecretWatchNamespace,
				kube_test.ClusterCredential{ClusterID: "c-good", Kubeconfig: []byte("kubeconfig-good")},
				kube_test.ClusterCredential{ClusterID: "c-bad", Kubeconfig: []byte("kubeconfig-bad")},
				kube_test.ClusterCredential{ClusterID: primaryClusterName, Kubeconfig: []byte("kubeconfig-primary")}),
			event:                        controllers_api.EventAdd,
			failingClusterID:             "c-bad",
			expectedAddedClusterIDList:   []cluster.ID{"c-good"},
			expectedDeletedClusterIDList: []cluster.ID{},
		},
	}

	logger := common.CreateNewLogger(zapcore.DebugLevel)
	clusterStore := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterStore.AddClusterForKey(SecretWatchNamespace+"/"+"secret2", &Cluster{ID: "c2", stop: make(chan struct{})})
	clusterStore.AddClusterForKey(SecretWatchNamespace+"/"+"secret2", &Cluster{ID: "c3", stop: make(chan struct{})}) // adding cluster c3 to cover stale cluster scenario

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cs := clusterStore
			sc := secretController{logger: logger, cs: cs, primaryClusterID: cluster.ID(primaryClusterName), secretReconcileTracker: NewSecretReconcileTracker()}

			var upsertSecretKey string
			var addedClusterIdList []cluster.ID
			var deletedClustersIdList []cluster.ID

			var createRemoteCluster = func(kubeConfig []byte, clusterID string) (*Cluster, error) {
				if tc.remoteClusterCreationError || clusterID == tc.failingClusterID {
					return nil, errors.New("failed to create remote cluster due to incorrect kubeconfig")
				}
				return &Cluster{ID: cluster.ID(clusterID)}, nil
			}
			var addCluster = func(secretKey string, cluster *Cluster) error {
				addedClusterIdList = append(addedClusterIdList, cluster.ID)
				upsertSecretKey = secretKey
				return nil
			}

			// delete cluster if exist
			var deleteCluster = func(secretKey string, clusterID cluster.ID) {
				sc.cs.GetExistingClustersForKey(secretKey)
				clusters := sc.cs.GetExistingClustersForKey(secretKey)

				for _, cls := range clusters {
					if cls.GetId() == clusterID {
						deletedClustersIdList = append(deletedClustersIdList, clusterID)
					}
				}
			}

			sc.createRemoteClusterHandler = createRemoteCluster
			sc.addClusterHandler = addCluster
			sc.deleteClusterHandler = deleteCluster
			sc.handleUpsert(tc.secretKey, tc.secret, tc.event)

			// assert Upsert Behaviour

			// check list of clusters added
			assert.Equal(t, true, reflect.DeepEqual(tc.expectedAddedClusterIDList, addedClusterIdList))

			// assert list of clusters deleted
			assert.Equal(t, true, assert.ElementsMatch(t, tc.expectedDeletedClusterIDList, deletedClustersIdList))

			// assert secret key which is added/updated
			if !tc.remoteClusterCreationError {
				assert.Equal(t, tc.secretKey, upsertSecretKey)
			}

		})
	}
}

func TestHandleDelete(t *testing.T) {
	primaryClusterName := "primary-cluster"
	testCases := []struct {
		name                          string
		secretName                    string
		primaryClusterExist           bool
		expectedDeletedClustersIdList []cluster.ID
	}{
		{
			name:                          "SecretExistWithOutPrimaryCluster",
			secretName:                    "secret1",
			primaryClusterExist:           true,
			expectedDeletedClustersIdList: []cluster.ID{"c1"},
		},
		{
			name:       "SecretDoesNotExist",
			secretName: "whatever",
		},
	}

	logger := common.CreateNewLogger(zapcore.DebugLevel)
	clusterStore := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterStore.AddClusterForKey("secret1", &Cluster{ID: "c1", stop: make(chan struct{})})
	clusterStore.AddClusterForKey("secret1", &Cluster{ID: cluster.ID(primaryClusterName)})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			var deletedClustersIdList []cluster.ID

			sc := secretController{logger: logger, cs: clusterStore, primaryClusterID: cluster.ID(primaryClusterName)}

			// mock deletion behaviour, record cluster id
			var deleteCluster = func(secretKey string, clusterID cluster.ID) {
				sc.cs.GetExistingClustersForKey(secretKey)
				clusters := sc.cs.GetExistingClustersForKey(secretKey)

				for _, cls := range clusters {
					if cls.GetId() == clusterID {
						deletedClustersIdList = append(deletedClustersIdList, clusterID)
					}
				}
			}

			sc.deleteClusterHandler = deleteCluster

			sc.handleDelete(tc.secretName)
			assert.Equal(t, tc.expectedDeletedClustersIdList, deletedClustersIdList)

		})
	}
}

func TestClusterExistsForOtherKey(t *testing.T) {
	primaryClusterName := "primary-cluster"
	testCases := []struct {
		name                  string
		clusterIDAlreadyExist bool
		expectedSecretKey     string
	}{
		{
			name:                  "SecretAlreadyExists",
			clusterIDAlreadyExist: true,
			expectedSecretKey:     "secret1",
		},
		{
			name:                  "SecretDoesNotExists",
			clusterIDAlreadyExist: false,
			expectedSecretKey:     "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			logger := common.CreateNewLogger(zapcore.DebugLevel)
			clusterStore := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			if tc.clusterIDAlreadyExist {
				clusterStore.AddClusterForKey("secret1", &Cluster{ID: "cluster1", stop: make(chan struct{})})
			}

			sc := secretController{logger: logger, cs: clusterStore, primaryClusterID: cluster.ID(primaryClusterName)}
			key, clusterExists := sc.cs.IsClusterExistsForOtherKey("secret2", "cluster1")

			assert.Equal(t, tc.expectedSecretKey, key)
			assert.Equal(t, tc.clusterIDAlreadyExist, clusterExists)
		})
	}
}

func TestGetClusterById(t *testing.T) {
	primaryCluster := &Cluster{ID: "primaryId"}
	nonPrimaryCluster := &Cluster{ID: "non-primary-1"}
	otherSecretCluster1 := &Cluster{ID: "other-secret-cluster-1"}
	otherSecretCluster2 := &Cluster{ID: "other-secret-cluster-2"}

	logger := common.CreateNewLogger(zapcore.DebugLevel)
	clusterStore := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(primaryCluster)

	clusterStore.AddClusterForKey("secret-1", nonPrimaryCluster)
	clusterStore.AddClusterForKey("secret-2", otherSecretCluster1)
	clusterStore.AddClusterForKey("secret-2", otherSecretCluster2)

	testCases := []struct {
		name            string
		lookupClusterId string
		expectedResult  *Cluster
	}{
		{
			name:            "Get primary cluster by ID",
			lookupClusterId: "primaryId",
			expectedResult:  primaryCluster,
		},
		{
			name:            "Single secret",
			lookupClusterId: "non-primary-1",
			expectedResult:  nonPrimaryCluster,
		},
		{
			name:            "Multiple secrets",
			lookupClusterId: "other-secret-cluster-2",
			expectedResult:  otherSecretCluster2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, foundCluster := clusterStore.GetClusterById(tc.lookupClusterId)

			assert.Equal(t, tc.expectedResult, foundCluster)
		})
	}
}
