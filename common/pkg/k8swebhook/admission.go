package k8swebhook

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"

	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"

	"go.uber.org/zap"
	"k8s.io/api/admission/v1beta1"
)

// Admittor defines the interface for handling a kubernetes admission request received from same/remote k8s cluster and building the accompanying response.
type Admittor interface {
	Admit(ar *v1beta1.AdmissionReview, clusterName string) *v1beta1.AdmissionResponse
}

type AdmittorV1 interface {
	Admit(ar *v1.AdmissionReview, clusterName string) *v1.AdmissionResponse
}

// admissionHandler maintains the state for the admission endpoint and implements the http.Handler interface.
type admissionHandler struct {
	logger *zap.SugaredLogger

	admittor Admittor
}

type admissionHandlerV1 struct {
	logger *zap.SugaredLogger

	admittorV1 AdmittorV1
}

// NewAdmissionHandler returns a new http.Handler instance that handles a kubernetes admission request.
func NewAdmissionHandler(logger *zap.SugaredLogger, admittor Admittor) http.Handler {
	return &admissionHandler{
		logger: logger,

		admittor: admittor,
	}
}

func NewAdmissionHandlerV1(logger *zap.SugaredLogger, admittorV1 AdmittorV1) http.Handler {
	return &admissionHandlerV1{
		logger: logger,

		admittorV1: admittorV1,
	}
}

func (h *admissionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		h.logger.Error("empty request body")
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	h.logger.Debugf("Request body: %s", string(body))

	// Verify that the content type is accurate.
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		h.logger.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	clusterName := ""
	if r.URL != nil {
		clusterName = parseCluster(r.URL.Path)
	}
	var admissionResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if _, _, err := Deserializer.Decode(body, nil, &ar); err != nil {
		h.logger.Errorw("failed to decode request body", "error", err)
		http.Error(w, "failed to decode request body", http.StatusBadRequest)
		return
	}
	admissionResponse = h.admittor.Admit(&ar, clusterName)

	admissionReview := v1beta1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		h.logger.Errorw("failed to encode response", "error", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
	if _, err := w.Write(resp); err != nil {
		h.logger.Errorw("failed to write response", "error", err)
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
}

func (h *admissionHandlerV1) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		h.logger.Error("empty request body")
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	h.logger.Debugf("Request body: %s", string(body))

	// Verify that the content type is accurate.
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		h.logger.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	clusterName := ""
	if r.URL != nil {
		clusterName = parseCluster(r.URL.Path)
	}
	ar, requestV1Beta1, err := decodeAdmissionReviewV1(body)
	if err != nil {
		h.logger.Errorw("failed to decode request body", "error", err)
		http.Error(w, "failed to decode request body", http.StatusBadRequest)
		return
	}
	admissionResponse := h.admittorV1.Admit(ar, clusterName)

	// Respond with the same AdmissionReview version the API server sent (v1 or v1beta1).
	// The API server requires the response version to match the request; otherwise it fails with conversion error.
	resp, err := buildAdmissionReviewResponseBody(ar, admissionResponse, requestV1Beta1)
	if err != nil {
		h.logger.Errorw("failed to encode response", "error", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(resp); err != nil {
		h.logger.Errorw("failed to write response", "error", err)
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
}

// buildAdmissionReviewResponseBody serializes the admission response in the same API version as the request.
func buildAdmissionReviewResponseBody(ar *v1.AdmissionReview, admissionResponse *v1.AdmissionResponse, respondV1Beta1 bool) ([]byte, error) {
	requestUID := types.UID(string(uuid.NewUUID())) // default to a new UID if the request has none
	if ar.Request != nil && ar.Request.UID != "" {
		requestUID = ar.Request.UID
	}
	if respondV1Beta1 {
		out := v1beta1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1beta1"},
		}
		if admissionResponse != nil {
			out.Response = convertV1ResponseToV1Beta1(admissionResponse)
			out.Response.UID = requestUID
		}
		return json.Marshal(out)
	}
	out := v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
	}
	if admissionResponse != nil {
		out.Response = admissionResponse
		out.Response.UID = requestUID
	}
	return json.Marshal(out)
}

// decodeAdmissionReviewV1 decodes the request body into a v1.AdmissionReview,
// accepting either admission.k8s.io/v1 or admission.k8s.io/v1beta1. When the API
// server sends v1beta1 (e.g. for older compatibility), we decode into v1beta1
// and convert to v1 so the admittor always receives a proper v1 type.
func decodeAdmissionReviewV1(body []byte) (*v1.AdmissionReview, bool, error) {
	var version struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return nil, false, err
	}
	switch version.APIVersion {
	case "admission.k8s.io/v1beta1":
		var arBeta v1beta1.AdmissionReview
		if _, _, err := Deserializer.Decode(body, nil, &arBeta); err != nil {
			return nil, false, err
		}
		return convertV1Beta1AdmissionReviewToV1(&arBeta), true, nil
	default:
		// v1 or any other version: decode into v1 (no TypeMeta preset so decoder accepts the request as-is).
		var ar v1.AdmissionReview
		if _, _, err := Deserializer.Decode(body, nil, &ar); err != nil {
			return nil, false, err
		}
		return &ar, false, nil
	}
}

// convertV1ResponseToV1Beta1 converts a v1 AdmissionResponse to v1beta1 so we can
// respond with the same API version the API server sent (required to avoid conversion errors).
func convertV1ResponseToV1Beta1(in *v1.AdmissionResponse) *v1beta1.AdmissionResponse {
	if in == nil {
		return nil
	}
	out := &v1beta1.AdmissionResponse{
		UID:              in.UID,
		Allowed:          in.Allowed,
		Patch:            in.Patch,
		AuditAnnotations: in.AuditAnnotations,
	}
	if in.PatchType != nil {
		pt := v1beta1.PatchType(*in.PatchType)
		out.PatchType = &pt
	}
	if in.Result != nil {
		out.Result = in.Result.DeepCopy()
	}
	return out
}

// convertV1Beta1AdmissionReviewToV1 converts a v1beta1 AdmissionReview to v1 so
// admittors always receive a consistent v1 type. Only request/response fields
// used by admittors are copied; TypeMeta is set to v1.
func convertV1Beta1AdmissionReviewToV1(in *v1beta1.AdmissionReview) *v1.AdmissionReview {
	out := &v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
	}
	if in.Request != nil {
		out.Request = &v1.AdmissionRequest{
			UID:                in.Request.UID,
			Kind:               in.Request.Kind,
			Resource:           in.Request.Resource,
			RequestKind:        (*metav1.GroupVersionKind)(in.Request.RequestKind),
			RequestResource:    (*metav1.GroupVersionResource)(in.Request.RequestResource),
			RequestSubResource: in.Request.RequestSubResource,
			Name:               in.Request.Name,
			Namespace:          in.Request.Namespace,
			Operation:          v1.Operation(in.Request.Operation),
			UserInfo:           in.Request.UserInfo,
			Object:             runtime.RawExtension{Raw: in.Request.Object.Raw},
			OldObject:          runtime.RawExtension{Raw: in.Request.OldObject.Raw},
			DryRun:             (*bool)(in.Request.DryRun),
			Options:            in.Request.Options,
		}
	}
	return out
}

// parseCluster parse cluster name from webhook url path
// Supports both validate and mutate webhook formats:
// eg. "/validate/cluster/cluster1" -> "cluster1"
// eg. "/mutate/shard/cluster/cluster1" -> "cluster1"
func parseCluster(path string) string {
	path = strings.TrimSuffix(path, "/")
	res := strings.Split(path, "/")
	clusterName := ""

	// Handle different URL patterns:
	// /validate/cluster/cluster_name (4 segments)
	// /mutate/shard/cluster/cluster_name (5 segments)
	if len(res) == 4 {
		clusterName = res[3]
	} else if len(res) == 5 && res[2] == "shard" && res[3] == "cluster" {
		clusterName = res[4]
	}

	return clusterName
}
