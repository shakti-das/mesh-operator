package secretdiscovery

import (
	"bytes"
	"fmt"

	"k8s.io/client-go/discovery"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// Client provides an interface to retrieve various kube clients and deal with some common kubectl operations
type Client interface {
	kubernetes.Interface
	// RESTConfig returns the Kubernetes rest.Config used to configure the clients
	RESTConfig() *rest.Config

	// Kube returns the core kube client
	Kube() kubernetes.Interface

	// Dynamic client.
	Dynamic() dynamic.Interface

	Discovery() discovery.DiscoveryInterface

	// ExecCmd exec command on a specific pod and returns the execution result
	ExecCmd(podName string, podNamespace string, containerName string, command []string) (string, error)
}

type client struct {
	kubernetes.Interface
	config *rest.Config

	// core kube client
	kube kubernetes.Interface

	// Dynamic client
	dynamic dynamic.Interface

	// Discovery client
	discovery discovery.DiscoveryInterface
}

// NewClient creates a Kubernetes client from the given rest config.
func NewClient(logger *zap.SugaredLogger, config *rest.Config) (Client, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		logger.Errorw("failed to create dynamic k8s api client", "error", err)
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Errorw("failed to create core k8s client", "error", err)
		return nil, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		logger.Errorw("failed to create discovery k8s api client", "error", err)
		return nil, err
	}

	return &client{config: config, kube: kubeClient, dynamic: dynamicClient, discovery: discoveryClient}, nil
}

func (c *client) RESTConfig() *rest.Config {
	return c.config
}

func (c *client) Kube() kubernetes.Interface {
	return c.kube
}

func (c *client) Dynamic() dynamic.Interface {
	return c.dynamic
}

func (c *client) Discovery() discovery.DiscoveryInterface {
	return c.discovery
}

func (c *client) ExecCmd(podName string, podNamespace string, containerName string, command []string) (string, error) {

	req := c.kube.CoreV1().RESTClient().Post().Resource("pods").Name(podName).
		Namespace(podNamespace).SubResource("exec").VersionedParams(&v1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	// Create a buffer to capture the command output
	var stdout, stderr bytes.Buffer

	exec, err := remotecommand.NewSPDYExecutor(c.config, "POST", req.URL())
	if err != nil {
		return "", err
	}
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})

	if err != nil {
		if len(stderr.String()) > 0 {
			err = fmt.Errorf("error exec'ing into %s/%s %s container: %w\n %s", podNamespace, podName, containerName, err, stderr.String())
		} else {
			err = fmt.Errorf("error exec'ing into %s/%s %s container: %w", podNamespace, podName, containerName, err)
		}
	}

	if err != nil {
		return "", err
	}

	return stdout.String(), nil
}
