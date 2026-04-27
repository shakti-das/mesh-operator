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

	"github.com/istio-ecosystem/mesh-operator/common/pkg/logging"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/types"
)

func TestAdmissionHandlerRequestErrors(t *testing.T) {
	logger, _ := logging.NewLogger(zap.DebugLevel)
	sugar := logger.Sugar()

	admissionHandler := NewAdmissionHandler(sugar, &recordingAdmittor{allowed: true})
	httpServer := httptest.NewServer(admissionHandler)
	defer httpServer.Close()

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
			res, err := http.Post(httpServer.URL, tc.contentType, bytes.NewBuffer([]byte(tc.body)))
			require.NoError(t, err)
			resV1, err := http.Post(httpServerV1.URL, tc.contentType, bytes.NewBuffer([]byte(tc.body)))
			require.NoError(t, err)

			assert.Equal(t, tc.expectedResponseCode, res.StatusCode)
			assert.Equal(t, tc.expectedResponseCode, resV1.StatusCode)

			body, err := ioutil.ReadAll(res.Body)
			require.NoError(t, err)
			bodyV1, err := ioutil.ReadAll(resV1.Body)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedResponseBody, strings.TrimSpace(string(body)))
			assert.Equal(t, tc.expectedResponseBody, strings.TrimSpace(string(bodyV1)))
		})
	}
}

func TestAdmissionHandlerCallsAdmittor(t *testing.T) {
	logger, _ := logging.NewLogger(zap.DebugLevel)
	sugar := logger.Sugar()

	admittor := &recordingAdmittor{allowed: true}
	admissionHandler := NewAdmissionHandler(sugar, admittor)
	httpServer := httptest.NewServer(admissionHandler)
	defer httpServer.Close()

	req := &v1beta1.AdmissionReview{
		Request: &v1beta1.AdmissionRequest{
			UID: types.UID(ksuid.New().String()),
		},
	}

	reqJSON, err := json.Marshal(req)
	require.NoError(t, err)

	res, err := http.Post(httpServer.URL, "application/json", bytes.NewBuffer([]byte(reqJSON)))
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)

	body, err := ioutil.ReadAll(res.Body)
	require.NoError(t, err)

	ar := v1beta1.AdmissionReview{}
	_, _, err = Deserializer.Decode(body, nil, &ar)
	require.NoError(t, err)

	assert.Equal(t, req.Request.UID, ar.Response.UID)
	assert.Equal(t, req, admittor.requests[0])
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

	res, err := http.Post(httpServer.URL, "application/json", bytes.NewBuffer([]byte(reqJSON)))
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

// TestAdmissionHandlerV1AcceptsV1Beta1Request verifies that when the API server
// sends a v1beta1 AdmissionReview (e.g. for compatibility), the handler decodes
// it, converts to v1, and the admittor receives a proper v1.AdmissionReview.
func TestAdmissionHandlerV1AcceptsV1Beta1Request(t *testing.T) {
	logger, _ := logging.NewLogger(zap.DebugLevel)
	sugar := logger.Sugar()

	admittor := &recordingAdmittorV1{allowed: true}
	admissionHandlerV1 := NewAdmissionHandlerV1(sugar, admittor)
	httpServer := httptest.NewServer(admissionHandlerV1)
	defer httpServer.Close()

	reqBeta := &v1beta1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1beta1",
		},
		Request: &v1beta1.AdmissionRequest{
			UID: types.UID(ksuid.New().String()),
		},
	}

	reqJSON, err := json.Marshal(reqBeta)
	require.NoError(t, err)

	res, err := http.Post(httpServer.URL, "application/json", bytes.NewBuffer([]byte(reqJSON)))
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, res.StatusCode)

	body, err := ioutil.ReadAll(res.Body)
	require.NoError(t, err)

	// Response must be v1beta1 to match the request (API server requires same version)
	arBeta := v1beta1.AdmissionReview{}
	_, _, err = Deserializer.Decode(body, nil, &arBeta)
	require.NoError(t, err)

	assert.Equal(t, "admission.k8s.io/v1beta1", arBeta.APIVersion)
	assert.Equal(t, reqBeta.Request.UID, arBeta.Response.UID)

	// Admittor should have received a v1.AdmissionReview with same request data (internal conversion)
	require.Len(t, admittor.requests, 1)
	got := admittor.requests[0]
	assert.Equal(t, "admission.k8s.io/v1", got.APIVersion)
	require.NotNil(t, got.Request)
	assert.Equal(t, reqBeta.Request.UID, got.Request.UID)
}

// TestDecodeAdmissionReviewV1_InvalidJSON verifies decode fails on invalid JSON.
func TestDecodeAdmissionReviewV1_InvalidJSON(t *testing.T) {
	_, _, err := decodeAdmissionReviewV1([]byte("not json"))
	require.Error(t, err)
}

// TestDecodeAdmissionReviewV1_V1Body verifies v1 request body decodes to v1.AdmissionReview.
func TestDecodeAdmissionReviewV1_V1Body(t *testing.T) {
	req := &v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request:  &v1.AdmissionRequest{UID: types.UID(ksuid.New().String())},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	ar, v1beta1, err := decodeAdmissionReviewV1(body)
	require.NoError(t, err)
	require.NotNil(t, ar)
	assert.False(t, v1beta1)
	assert.Equal(t, "admission.k8s.io/v1", ar.APIVersion)
	require.NotNil(t, ar.Request)
	assert.Equal(t, req.Request.UID, ar.Request.UID)
}

// TestDecodeAdmissionReviewV1_V1Beta1Body verifies v1beta1 body is decoded and converted to v1.
func TestDecodeAdmissionReviewV1_V1Beta1Body(t *testing.T) {
	req := &v1beta1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1beta1"},
		Request:  &v1beta1.AdmissionRequest{UID: types.UID(ksuid.New().String())},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	ar, v1beta1, err := decodeAdmissionReviewV1(body)
	require.NoError(t, err)
	require.NotNil(t, ar)
	assert.True(t, v1beta1)
	assert.Equal(t, "admission.k8s.io/v1", ar.APIVersion)
	require.NotNil(t, ar.Request)
	assert.Equal(t, req.Request.UID, ar.Request.UID)
}

// TestDecodeAdmissionReviewV1_UnknownVersion verifies unknown apiVersion falls through to default (decode as v1, return respondV1Beta1 false).
func TestDecodeAdmissionReviewV1_UnknownVersion(t *testing.T) {
	body := []byte(`{"apiVersion":"admission.k8s.io/v2","kind":"AdmissionReview"}`)
	ar, respondV1Beta1, err := decodeAdmissionReviewV1(body)
	require.NoError(t, err)
	require.NotNil(t, ar)
	assert.False(t, respondV1Beta1, "unknown version should not be treated as v1beta1")
}

// TestConvertV1Beta1AdmissionReviewToV1_NilRequest verifies nil request yields nil out.Request.
func TestConvertV1Beta1AdmissionReviewToV1_NilRequest(t *testing.T) {
	in := &v1beta1.AdmissionReview{Request: nil}
	out := convertV1Beta1AdmissionReviewToV1(in)
	require.NotNil(t, out)
	assert.Equal(t, "admission.k8s.io/v1", out.APIVersion)
	assert.Nil(t, out.Request)
}

// TestConvertV1Beta1AdmissionReviewToV1_WithRequest verifies request fields are copied.
func TestConvertV1Beta1AdmissionReviewToV1_WithRequest(t *testing.T) {
	uid := types.UID(ksuid.New().String())
	in := &v1beta1.AdmissionReview{
		Request: &v1beta1.AdmissionRequest{
			UID:       uid,
			Namespace: "test-ns",
			Name:      "test-name",
		},
	}
	out := convertV1Beta1AdmissionReviewToV1(in)
	require.NotNil(t, out)
	assert.Equal(t, "admission.k8s.io/v1", out.APIVersion)
	require.NotNil(t, out.Request)
	assert.Equal(t, uid, out.Request.UID)
	assert.Equal(t, "test-ns", out.Request.Namespace)
	assert.Equal(t, "test-name", out.Request.Name)
}

// TestBuildAdmissionReviewResponseBody verifies response body is built in the correct version.
func TestBuildAdmissionReviewResponseBody(t *testing.T) {
	uid := types.UID(ksuid.New().String())
	ar := &v1.AdmissionReview{Request: &v1.AdmissionRequest{UID: uid}}
	allowedResp := &v1.AdmissionResponse{Allowed: true}
	deniedResp := &v1.AdmissionResponse{Allowed: false, Result: &metav1.Status{Message: "denied"}}

	t.Run("v1 response", func(t *testing.T) {
		body, err := buildAdmissionReviewResponseBody(ar, allowedResp, false)
		require.NoError(t, err)
		var out v1.AdmissionReview
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Equal(t, "admission.k8s.io/v1", out.APIVersion)
		require.NotNil(t, out.Response)
		assert.Equal(t, uid, out.Response.UID)
		assert.True(t, out.Response.Allowed)
	})
	t.Run("v1beta1 response", func(t *testing.T) {
		body, err := buildAdmissionReviewResponseBody(ar, allowedResp, true)
		require.NoError(t, err)
		var out v1beta1.AdmissionReview
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Equal(t, "admission.k8s.io/v1beta1", out.APIVersion)
		require.NotNil(t, out.Response)
		assert.Equal(t, uid, out.Response.UID)
		assert.True(t, out.Response.Allowed)
	})
	t.Run("v1beta1 denial with Result", func(t *testing.T) {
		body, err := buildAdmissionReviewResponseBody(ar, deniedResp, true)
		require.NoError(t, err)
		var out v1beta1.AdmissionReview
		require.NoError(t, json.Unmarshal(body, &out))
		assert.False(t, out.Response.Allowed)
		require.NotNil(t, out.Response.Result)
		assert.Equal(t, "denied", out.Response.Result.Message)
	})
	t.Run("nil response", func(t *testing.T) {
		body, err := buildAdmissionReviewResponseBody(ar, nil, false)
		require.NoError(t, err)
		var out v1.AdmissionReview
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Equal(t, "admission.k8s.io/v1", out.APIVersion)
		assert.Nil(t, out.Response)
	})
	t.Run("nil response v1beta1", func(t *testing.T) {
		body, err := buildAdmissionReviewResponseBody(ar, nil, true)
		require.NoError(t, err)
		var out v1beta1.AdmissionReview
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Equal(t, "admission.k8s.io/v1beta1", out.APIVersion)
		assert.Nil(t, out.Response)
	})
	t.Run("request nil uses generated UID", func(t *testing.T) {
		arNoRequest := &v1.AdmissionReview{Request: nil}
		body, err := buildAdmissionReviewResponseBody(arNoRequest, allowedResp, false)
		require.NoError(t, err)
		var out v1.AdmissionReview
		require.NoError(t, json.Unmarshal(body, &out))
		require.NotNil(t, out.Response)
		assert.NotEmpty(t, out.Response.UID, "response UID should be generated when request has no UID")
	})
	t.Run("request UID empty uses generated UID", func(t *testing.T) {
		arEmptyUID := &v1.AdmissionReview{Request: &v1.AdmissionRequest{UID: ""}}
		body, err := buildAdmissionReviewResponseBody(arEmptyUID, allowedResp, true)
		require.NoError(t, err)
		var out v1beta1.AdmissionReview
		require.NoError(t, json.Unmarshal(body, &out))
		require.NotNil(t, out.Response)
		assert.NotEmpty(t, out.Response.UID, "response UID should be generated when request UID is empty")
	})
}

// TestConvertV1ResponseToV1Beta1 covers conversion branches.
func TestConvertV1ResponseToV1Beta1(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		assert.Nil(t, convertV1ResponseToV1Beta1(nil))
	})
	t.Run("with Result", func(t *testing.T) {
		in := &v1.AdmissionResponse{Allowed: false, Result: &metav1.Status{Code: 400, Message: "policy violation"}}
		out := convertV1ResponseToV1Beta1(in)
		require.NotNil(t, out)
		assert.False(t, out.Allowed)
		require.NotNil(t, out.Result)
		assert.Equal(t, int32(400), out.Result.Code)
		assert.Equal(t, "policy violation", out.Result.Message)
	})
	t.Run("with Patch and PatchType", func(t *testing.T) {
		pt := v1.PatchTypeJSONPatch
		in := &v1.AdmissionResponse{Allowed: true, Patch: []byte(`[]`), PatchType: &pt}
		out := convertV1ResponseToV1Beta1(in)
		require.NotNil(t, out)
		assert.Equal(t, in.Patch, out.Patch)
		require.NotNil(t, out.PatchType)
		assert.Equal(t, v1beta1.PatchTypeJSONPatch, *out.PatchType)
	})
	t.Run("allowed only no PatchType or Result", func(t *testing.T) {
		in := &v1.AdmissionResponse{Allowed: true}
		out := convertV1ResponseToV1Beta1(in)
		require.NotNil(t, out)
		assert.True(t, out.Allowed)
		assert.Nil(t, out.PatchType)
		assert.Nil(t, out.Result)
	})
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
		{
			"ParseMutateShardCluster",
			"/mutate/shard/cluster/my-cluster",
			"my-cluster",
		},
	}
	for _, tc := range testCases {
		clusterName := parseCluster(tc.url)
		assert.Equal(t, tc.expectedClusterName, clusterName)
	}
}

type recordingAdmittor struct {
	allowed  bool
	requests []*v1beta1.AdmissionReview
}

type recordingAdmittorV1 struct {
	allowed  bool
	requests []*v1.AdmissionReview
}

func (a *recordingAdmittor) Admit(ar *v1beta1.AdmissionReview, _ string) *v1beta1.AdmissionResponse {
	a.requests = append(a.requests, ar)

	return &v1beta1.AdmissionResponse{
		Allowed: a.allowed,
	}
}

func (a *recordingAdmittorV1) Admit(ar *v1.AdmissionReview, _ string) *v1.AdmissionResponse {
	a.requests = append(a.requests, ar)

	return &v1.AdmissionResponse{
		Allowed: a.allowed,
	}
}
