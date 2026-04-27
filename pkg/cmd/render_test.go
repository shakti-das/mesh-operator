package cmd

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultArguments(t *testing.T) {
	cmd := NewTemplateRendererCommand()

	templatePaths, err := cmd.Flags().GetStringSlice("template-paths")
	assert.Nil(t, err)
	assert.Equal(t, []string{}, templatePaths, "Unexpected templatePaths")

	servicePath, err := cmd.Flags().GetString("service-path")
	assert.Nil(t, err)
	assert.Equal(t, "", servicePath, "Unexpected servicePath")

	serviceEntryPath, err := cmd.Flags().GetString("service-entry-path")
	assert.Nil(t, err)
	assert.Equal(t, "", serviceEntryPath, "Unexpected serviceEntryPath")

	ingressPath, err := cmd.Flags().GetString("ingress-path")
	assert.Nil(t, err)
	assert.Equal(t, "", ingressPath, "Unexpected ingressPath")

	serverlessServicePath, err := cmd.Flags().GetString("serverless-service-path")
	assert.Nil(t, err)
	assert.Equal(t, "", serverlessServicePath, "Unexpected serverlessServicePath")

	metadata, err := cmd.Flags().GetStringToString("metadata")
	assert.Nil(t, err)
	assert.Equal(t, map[string]string{}, metadata, "Unexpected metadata")

	printTemplates, err := cmd.Flags().GetBool("print-templates")
	assert.Nil(t, err)
	if printTemplates == true {
		t.Fatalf("Unexpected printTemplates. want false, got true")
	}
}

func TestRenderCommandFlow(t *testing.T) {
	testCases := []struct {
		name                  string
		expectedError         error
		servicePath           string
		serviceEntryPath      string
		ingressPath           string
		serverlessServicePath string
		mopPath               string
		ctpPath               string
		patcher               string
	}{
		{
			name:          "RenderSvcWithFormatPatcher",
			expectedError: nil,
			servicePath:   "../testdata/service-files/service.yaml",
			patcher:       "true",
		},
		{
			name:          "RenderSvcWithCTP",
			expectedError: nil,
			servicePath:   "../testdata/service-files/service.yaml",
			ctpPath:       "../testdata/ctp-files/ctp.yaml",
			patcher:       "true",
		},
		{
			name:             "RenderServiceEntryWithFormatPatcher",
			expectedError:    nil,
			serviceEntryPath: "../testdata/service-entry-files/se.yaml",
			patcher:          "true",
		},
		{
			name:          "RenderMopWithFormatPatcher",
			expectedError: nil,
			servicePath:   "../testdata/service-files/service.yaml",
			mopPath:       "../testdata/mop-files/mop.yaml",
			patcher:       "true",
		},
		{
			name:          "RenderSvcWithConsolePatcher",
			expectedError: nil,
			servicePath:   "../testdata/service-files/service.yaml",
			patcher:       "false",
		},
		{
			name:          "RenderMopWithConsolePatcher",
			expectedError: nil,
			servicePath:   "../testdata/service-files/service.yaml",
			mopPath:       "../testdata/mop-files/mop.yaml",
			patcher:       "false",
		},
		{
			name:                  "ErrorIfServiceServiceEntryIngressAndServerlessServicePathIsProvided",
			expectedError:         errors.New("multiple resource paths provided. only one of servicePath, serviceEntryPath, ingressPath, or serverlessServicePath should be provided for rendering"),
			servicePath:           "/alpha",
			serviceEntryPath:      "/beta",
			ingressPath:           "/gamma",
			serverlessServicePath: "/delta",
			patcher:               "false",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			command := NewTemplateRendererCommand()
			command.SetArgs([]string{
				"--service-path=" + tc.servicePath,
				"--service-entry-path=" + tc.serviceEntryPath,
				"--ingress-path=" + tc.ingressPath,
				"--serverless-service-path=" + tc.serverlessServicePath,
				"--meshoperator-path=" + tc.mopPath,
				"--service-policy-path=" + tc.ctpPath,
				"--template-paths=" + TemplatesLocation,
				"--print-templates=" + tc.patcher,
				"--default-template-type=Service=default/default,ServiceEntry=default/external-service,ServerlessService=default/knative-serverless"})

			actualErr := command.Execute()
			assert.Equal(t, tc.expectedError, actualErr)
		})
	}
}

func TestEmptyServiceFilePath(t *testing.T) {
	expectedErrorString := "exit status 2"
	// Run the crashing code when FLAG is set
	if os.Getenv("FLAG") == "1" {
		command := NewTemplateRendererCommand()
		command.SetArgs([]string{
			"--service-path=",
			"--template-paths=" + TemplatesLocation,
			"--print-templates=" + "true"})
		_ = command.Execute()
		return
	}

	// Run the test in a subprocess
	e, ok := runTestInSubprocess(t.Name())

	assert.Equal(t, true, ok)
	assert.Equal(t, expectedErrorString, e.Error())
}

func TestIncorrectServiceFilePath(t *testing.T) {
	expectedErrorString := "exit status 2"

	// Run the crash code when FLAG is set
	if os.Getenv("FLAG") == "1" {
		command := NewTemplateRendererCommand()
		command.SetArgs([]string{
			"--service-path=/alpha/beta",
			"--template-paths=" + TemplatesLocation,
			"--print-templates=" + "true"})
		_ = command.Execute()
		return
	}

	// Run the test in a subprocess
	e, ok := runTestInSubprocess(t.Name())

	assert.Equal(t, true, ok)
	assert.Equal(t, expectedErrorString, e.Error())
}

func TestUnmarshalFailure(t *testing.T) {
	expectedErrorString := "exit status 2"

	// Run the crash code when FLAG is set
	if os.Getenv("FLAG") == "1" {
		command := NewTemplateRendererCommand()
		command.SetArgs([]string{
			"--service-path=../testdata/end-to-end-templates/default_default_destinationrule.jsonnet",
			"--template-paths=" + TemplatesLocation,
			"--print-templates=" + "true"})
		_ = command.Execute()
		return
	}

	// Run the test in a subprocess
	e, ok := runTestInSubprocess(t.Name())

	assert.Equal(t, true, ok)
	assert.Equal(t, expectedErrorString, e.Error())
}

func TestIncorrectMopFilePath(t *testing.T) {
	expectedErrorString := "exit status 2"
	//Run the crashing code when FLAG is set
	if os.Getenv("FLAG") == "1" {
		command := NewTemplateRendererCommand()
		command.SetArgs([]string{
			"--template-paths=" + TemplatesLocation,
			"--meshoperator-path=/alpha/beta",
			"--service-path=../testdata/service-files/service.yaml"})
		_ = command.Execute()
		return
	}

	e, ok := runTestInSubprocess(t.Name())

	assert.Equal(t, true, ok)
	assert.Equal(t, expectedErrorString, e.Error())
}

func runTestInSubprocess(testName string) (error, bool) {
	cmd := exec.Command(os.Args[0], "-test.run="+testName)
	cmd.Env = append(os.Environ(), "FLAG=1")
	err := cmd.Run()

	e, ok := err.(*exec.ExitError)
	return e, ok
}
