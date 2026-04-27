package k8swebhook

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"

	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.uber.org/zap"
)

type AdmittorV1 interface {
	Admit(ar *v1.AdmissionReview, clusterName string) *v1.AdmissionResponse
}

type admissionHandlerV1 struct {
	logger *zap.SugaredLogger

	admittorV1 AdmittorV1
}

func NewAdmissionHandlerV1(logger *zap.SugaredLogger, admittorV1 AdmittorV1) http.Handler {
	return &admissionHandlerV1{
		logger: logger,

		admittorV1: admittorV1,
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
	var admissionResponse *v1.AdmissionResponse
	ar := v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
	}
	if _, _, err := Deserializer.Decode(body, nil, &ar); err != nil {
		h.logger.Errorw("failed to decode request body", "error", err)
		http.Error(w, "failed to decode request body", http.StatusBadRequest)
		return
	}
	admissionResponse = h.admittorV1.Admit(&ar, clusterName)

	admissionReview := v1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
	}
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

// parseCluster parse new envs from validate url path
// eg. "/validate/cluster/cluster1"
func parseCluster(path string) string {
	path = strings.TrimSuffix(path, "/")
	res := strings.Split(path, "/")
	clusterName := ""
	if len(res) == 4 {
		clusterName = res[3]
	}
	return clusterName
}
