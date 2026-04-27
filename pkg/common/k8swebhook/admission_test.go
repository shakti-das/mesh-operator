package k8swebhook

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/logging"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/types"
)

func TestAdmissionHandlerRequestErrors(t *testing.T) {
	logger, _ := logging.NewLogger(zap.DebugLevel)
	sugar := logger.Sugar()

	admissionHandlerV1 := NewAdmissionHandlerV1(sugar, &recordingAdmittorV1{allowed: true})
	httpServerV1 := httptest.NewServer(admissionHandlerV1)
	defer httpServerV1.Close()

	testCases := []struct {
		name                 string
		contentType          string
		body                 string
		expectedResponseCode int
		expectedResponseBody string
	}{
		{
			name:                 "EmptyBody",
			contentType:          "application/json",
			body:                 "",
			expectedResponseCode: http.StatusBadRequest,
			expectedResponseBody: "empty request body",
		},
		{
			name:                 "UnsupportedContentType",
			contentType:          "plain/text",
			body:                 "test",
			expectedResponseCode: http.StatusUnsupportedMediaType,
			expectedResponseBody: "invalid Content-Type, expect `application/json`",
		},
		{
			name:                 "BadAdmissionRequest",
			contentType:          "application/json",
			body:                 "test",
			expectedResponseCode: http.StatusBadRequest,
			expectedResponseBody: "failed to decode request body",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resV1, err := http.Post(httpServerV1.URL, tc.contentType, bytes.NewBuffer([]byte(tc.body)))
			require.NoError(t, err)

			assert.Equal(t, tc.expectedResponseCode, resV1.StatusCode)

			bodyV1, err := ioutil.ReadAll(resV1.Body)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedResponseBody, strings.TrimSpace(string(bodyV1)))
		})
	}
}

func TestAdmissionHandlerCallsAdmittorV1(t *testing.T) {
	logger, _ := logging.NewLogger(zap.DebugLevel)
	sugar := logger.Sugar()

	admittor := &recordingAdmittorV1{allowed: true}
	admissionHandlerV1 := NewAdmissionHandlerV1(sugar, admittor)
	httpServer := httptest.NewServer(admissionHandlerV1)
	defer httpServer.Close()

	req := &v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Request: &v1.AdmissionRequest{
			UID: types.UID(ksuid.New().String()),
		},
	}

	reqJSON, err := json.Marshal(req)
	require.NoError(t, err)

	res, err := http.Post(httpServer.URL, "application/json", bytes.NewBuffer(reqJSON))
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)

	body, err := ioutil.ReadAll(res.Body)
	require.NoError(t, err)

	ar := v1.AdmissionReview{}
	_, _, err = Deserializer.Decode(body, nil, &ar)
	require.NoError(t, err)

	assert.Equal(t, req.TypeMeta, ar.TypeMeta)
	assert.Equal(t, req.Request.UID, ar.Response.UID)
	assert.Equal(t, req, admittor.requests[0])
}

func TestParseCluster(t *testing.T) {
	testCases := []struct {
		name                string
		url                 string
		expectedClusterName string
	}{
		{
			"ParseValidCluster",
			"/validate/cluster/cluster1",
			"cluster1",
		},
		{
			"ParseEmptyCluster",
			"/validate",
			"",
		},
	}
	for _, tc := range testCases {
		clusterName := parseCluster(tc.url)
		assert.Equal(t, tc.expectedClusterName, clusterName)
	}
}

type recordingAdmittorV1 struct {
	allowed  bool
	requests []*v1.AdmissionReview
}

func (a *recordingAdmittorV1) Admit(ar *v1.AdmissionReview, _ string) *v1.AdmissionResponse {
	a.requests = append(a.requests, ar)

	return &v1.AdmissionResponse{
		Allowed: a.allowed,
	}
}
