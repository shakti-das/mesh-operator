package error

import (
	"errors"
	"fmt"
	"testing"
)

func TestControllerReconcileGenericErrorImpl_ShouldReportAsError(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		want  bool
	}{
		{
			name:  "nil cause should report",
			cause: nil,
			want:  true,
		},
		{
			name:  "standard error should report",
			cause: errors.New("standard error"),
			want:  true,
		},
		{
			name:  "user config error should not report",
			cause: &UserConfigError{Message: "invalid config"},
			want:  false,
		},
		{
			name:  "non-critical reconcile error should not report",
			cause: &NonCriticalReconcileError{Message: "optimistic lock error"},
			want:  false,
		},
		{
			name: "overlaying error should not report",
			cause: &OverlayingErrorImpl{
				ErrorMap: map[string]*OverlayErrorInfo{
					"mop1": {
						OverlayIndex: 1,
						Message:      "overlay failed",
					},
				},
			},
			want: false,
		},
		{
			name:  "critical no retry error should report",
			cause: &CriticalNoRetryError{Message: "critical error"},
			want:  true,
		},
		{
			name:  "wrapped standard error should report",
			cause: fmt.Errorf("wrapped: %w", errors.New("inner error")),
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ControllerReconcileGenericErrorImpl{
				Message: "test error",
				Cause:   tt.cause,
			}
			if got := e.ShouldReportAsError(); got != tt.want {
				t.Errorf("ControllerReconcileGenericErrorImpl.ShouldReportAsError() = %v, want %v", got, tt.want)
			}
		})
	}
}
