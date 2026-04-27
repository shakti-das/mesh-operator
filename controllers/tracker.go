package controllers

import (
	"strings"
	"sync"

	"go.uber.org/zap"
)

const (
	KeyDelimiter = "/"
)

type ReconcileManager interface {
	trackKey(keys ...string) bool
	unTrackKey(ctxLogger *zap.SugaredLogger, clusterName string, keys ...string)
}

type multiClusterReconcileManager struct {
	sync.RWMutex

	logger *zap.SugaredLogger

	// tracks key reconciliation across clusters
	keyMapper map[string]struct{}
}

func NewMultiClusterReconcileTracker(logger *zap.SugaredLogger) ReconcileManager {
	return &multiClusterReconcileManager{
		logger:    logger,
		keyMapper: make(map[string]struct{}),
	}
}

// trackKey - track or detect key reconciliation across clusters
func (m *multiClusterReconcileManager) trackKey(keyParts ...string) bool {
	m.Lock()
	defer m.Unlock()
	key := strings.Join(keyParts, KeyDelimiter)
	_, hasKey := m.keyMapper[key]
	// if key not getting reconciled by any other controller - track key and return
	if !hasKey {
		m.keyMapper[key] = struct{}{}
		return false
	}
	return true
}

func (m *multiClusterReconcileManager) unTrackKey(ctxLogger *zap.SugaredLogger, clusterName string, keyParts ...string) {
	m.Lock()
	defer m.Unlock()
	key := strings.Join(keyParts, KeyDelimiter)
	ctxLogger.Infof("removing key(%s) from reconcile tracker for cluster: %s", key, clusterName)
	delete(m.keyMapper, key)
}

// A reconcile manager that does nothing
type NoOpReconcileManager struct{}

// trackKey - always allow reconcile
func (rm *NoOpReconcileManager) trackKey(_ ...string) bool {
	return false
}

func (rm *NoOpReconcileManager) unTrackKey(_ *zap.SugaredLogger, _ string, _ ...string) {
}
