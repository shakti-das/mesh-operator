package hash

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"go.uber.org/zap"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"k8s.io/apimachinery/pkg/util/rand"
)

// ComputeHashForName hashes using fnv32a algorithm
func ComputeHashForName(collisionCount *int32, nameParts ...string) string {
	name := strings.Join(nameParts, constants.TemplateKeyDelimiter)
	podTemplateSpecHasher := fnv.New32a()
	_, _ = podTemplateSpecHasher.Write([]byte(name))

	// Add collisionCount in the hash if it exists.
	if collisionCount != nil {
		collisionCountBytes := make([]byte, 8)
		binary.LittleEndian.PutUint32(collisionCountBytes, uint32(*collisionCount))
		_, _ = podTemplateSpecHasher.Write(collisionCountBytes)
	}
	return rand.SafeEncodeString(fmt.Sprint(podTemplateSpecHasher.Sum32()))
}

// ComputeObjectHash - service-hash computation and conflict detection
//
// ServiceHash build as hash_of(kind, service-namespace, service-name, collision-count) in case of ServiceEntry Resource whereas
// hash_of(service-namespace, service-name, collision-count) in case of ServiceResource
// where collision count increments if conflicts have been detected
// Collision lookup is done for objects of given type(GVR) by namespace/name
// where lookup is done in the service namespace - for ordinary extensions
// and ConfigNamespace - for config-namespaced extensions
func ComputeObjectHash(logger *zap.SugaredLogger, configNamespaceExtension bool, configNamespace string, mopName string, objectKind string, objectNamespace string, objectName string, kubeClient kube.Client, groupVersionKind schema.GroupVersionKind) (string, error) {
	var collisionCounter int32 = 0

	hashCalculator := func(calcCollision *int32, calcNamespace, calcName string) string {
		var nameParts []string
		// In the v0 version of hash logic, hash logic used to account only for service resource
		// now in the latest version, for non-Service resources 'Kind' is included in hashing logic to differentiate from Service Resource
		if objectKind != "" && objectKind != constants.ServiceKind.Kind {
			nameParts = append(nameParts, objectKind)
		}

		if configNamespaceExtension {
			nameParts = append(nameParts, calcNamespace)
		}
		nameParts = append(nameParts, calcName)

		return ComputeHashForName(calcCollision, nameParts...)
	}

	objectHash := hashCalculator(nil, objectNamespace, objectName)

	// "0" is used explicitly because filter with index "0" is bound to exist in cluster if in case collision exist
	filterLookUpName := ComposeExtensionName(mopName, objectHash, "0")
	lookupNamespace := objectNamespace
	if configNamespaceExtension {
		lookupNamespace = configNamespace
	}

	for {
		// check if a collision exist
		collisionExist, err := collisionExists(logger, configNamespaceExtension, objectKind, objectNamespace, objectName, lookupNamespace, filterLookUpName, kubeClient, groupVersionKind)
		if err != nil {
			return "", err
		}
		if !collisionExist {
			return objectHash, nil
		} else {
			collisionCounter = collisionCounter + 1
			objectHash = hashCalculator(&collisionCounter, objectNamespace, objectName)
			filterLookUpName = ComposeExtensionName(mopName, objectHash, "0")
		}
	}
}
