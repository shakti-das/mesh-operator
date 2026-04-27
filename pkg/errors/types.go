package error

import (
	"fmt"
)

// UserConfigError - an error caused by an issue in user supplied config
// Shouldn't be retried or recorded as a reconciliation error.
type UserConfigError struct {
	Message string
}

func (m *UserConfigError) Error() string {
	return m.Message
}

// NonCriticalReconcileError - an error typically caused by an optimistic locking error
// Should be retried and not recorded as a reconciliation error
type NonCriticalReconcileError struct {
	Message string
}

func (m *NonCriticalReconcileError) Error() string {
	return m.Message
}

type CriticalNoRetryError struct {
	Message string
}

func (m CriticalNoRetryError) Error() string {
	return m.Message
}

type OverlayErrorInfo struct {
	OverlayIndex int
	Message      string
}

// OverlayingError error raised during config generation by the overlayer
type OverlayingError interface {
	Error() string
}

// OverlayingErrorImpl - collection of errors raised when MOPs attempt to overlay configs generated for a service
type OverlayingErrorImpl struct {
	ErrorMap map[string]*OverlayErrorInfo
}

func (m *OverlayingErrorImpl) Error() string {
	msg := "error encountered when overlaying:"
	for mopName, errInfo := range m.ErrorMap {
		msg += fmt.Sprintf(" mop %s, overlay <%d>: %v;", mopName, errInfo.OverlayIndex, errInfo.Message)
	}
	return msg[0 : len(msg)-1]
}

type ControllerReconcileGenericError interface {
	Error() string
	GetCause() error
	ShouldReportAsError() bool
}

type ControllerReconcileGenericErrorImpl struct {
	Message string
	Cause   error
}

func (e *ControllerReconcileGenericErrorImpl) Error() string {
	return fmt.Sprintf(e.Message+" cause: %s", e.Cause.Error())
}

func (e *ControllerReconcileGenericErrorImpl) ShouldReportAsError() bool {
	return !IsOverlayingError(e.Cause) && !IsUserConfigError(e.Cause) && !IsNonCriticalReconcileError(e.Cause)
}

func (e *ControllerReconcileGenericErrorImpl) GetCause() error {
	return e.Cause
}

func IsUserConfigError(err error) bool {
	_, userConfigError := err.(*UserConfigError)
	return userConfigError
}

func IsNonCriticalReconcileError(err error) bool {
	_, nonCriticalReconcileError := err.(*NonCriticalReconcileError)
	return nonCriticalReconcileError
}

func IsOverlayingError(err error) bool {
	_, overlayingError := err.(*OverlayingErrorImpl)
	return overlayingError
}

func IsCriticalNoRetryError(err error) bool {
	_, criticalNoRetryError := err.(*CriticalNoRetryError)
	return criticalNoRetryError
}
