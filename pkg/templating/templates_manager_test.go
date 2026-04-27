package templating

import (
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

const TemplateNameSeparator = constants.TemplateNameSeparator

func TestGetTemplatesByPrefix(t *testing.T) {
	templates := map[string]string{
		"default_default_virtualservice":       "default-vs-content",
		"default_default_destinationrule":      "default-dr-content",
		"other-ns_other-svc_virtualservice":    "other-vs-content",
		"other-ns_other-svc_destinationrule":   "other-dr-content",
		"example-coreapp_coreapp_vs":           "coreapp-content",
		"example-coreapp_coreapp-bootstrap_vs": "coreapp-bootstrap-content",
	}
	testCases := []struct {
		name            string
		lookupNamespace string
		lookupService   string
		expectedResult  map[string]string
	}{
		{
			name:            "Normal Lookup",
			lookupNamespace: "other-ns",
			lookupService:   "other-svc",
			expectedResult: map[string]string{
				"other-ns_other-svc_virtualservice":  "other-vs-content",
				"other-ns_other-svc_destinationrule": "other-dr-content",
			},
		},
		{
			name:            "Two template types exists with common prefix",
			lookupNamespace: "example-coreapp",
			lookupService:   "coreapp",
			expectedResult: map[string]string{
				"example-coreapp_coreapp_vs": "coreapp-content",
			},
		},
		{
			name:            "MissingLookup",
			lookupNamespace: "unknown",
			lookupService:   "unknown",
			expectedResult:  map[string]string{},
		},
	}

	manager := filesystemTemplatesManager{
		logger:     zaptest.NewLogger(t).Sugar(),
		templates:  templates,
		cacheMutex: &sync.RWMutex{},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			templateLookupPrefix := tc.lookupNamespace + TemplateKeyDelimiter + tc.lookupService + TemplateKeyDelimiter
			templatesFound := manager.GetTemplatesByPrefix(templateLookupPrefix)
			assert.Equal(t, tc.expectedResult, templatesFound)
		})
	}
}

func TestLoadTemplatesFailure(t *testing.T) {
	testFs := fakeFs{
		error: "Failed to read dir!",
	}
	manager := filesystemTemplatesManager{
		logger:        zaptest.NewLogger(t).Sugar(),
		fs:            &testFs,
		templatePaths: []string{"/template/path"},
	}

	_, err := manager.reloadTemplates()
	assert.NotNil(t, err, "Error expected")
	assert.Equal(t, "Failed to read dir!", err.Error())
}

func TestLoadTemplatesFromFiles(t *testing.T) {
	testFs := fakeFs{
		readFileContent: []byte("file-content"),
		dirs: map[string][]fs.FileInfo{
			"/templates/path": {
				&fakeFile{name: "default_default_virtualservice.jsonnet"},
				&fakeFile{name: "default_default_destinationrule.jsonnet"},
				&fakeFile{name: "default_default_bad.bad"},
				&fakeFile{name: "..bad-file-name.bad"},
			},
			"/other/templates/path": {
				&fakeFile{name: "default_default_virtualservice.jsonnet"},
				&fakeFile{name: "default_default_destinationrule.jsonnet"},
				&fakeFile{name: "default_default_bad.bad"},
				&fakeFile{name: "..bad-file-name.bad"},
			},
		},
	}
	manager := filesystemTemplatesManager{
		logger:     zaptest.NewLogger(t).Sugar(),
		fs:         &testFs,
		cacheMutex: &sync.RWMutex{},
		templatePaths: []string{
			"/templates/path",
			"/other/templates/path",
		},
	}

	templates, err := manager.reloadTemplates()
	sort.Strings(testFs.dirsRead)
	sort.Strings(testFs.filesRead)

	assert.Nil(t, err)
	assert.Equal(
		t,
		[]string{
			"/other/templates/path",
			"/templates/path"},
		testFs.dirsRead)
	assert.Equal(
		t,
		[]string{
			"/other/templates/path/default_default_destinationrule.jsonnet",
			"/other/templates/path/default_default_virtualservice.jsonnet",
			"/templates/path/default_default_destinationrule.jsonnet",
			"/templates/path/default_default_virtualservice.jsonnet",
		},
		testFs.filesRead)
	assert.Equal(
		t,
		map[string]string{
			"default_default_virtualservice":  "file-content",
			"default_default_destinationrule": "file-content",
		},
		templates)
}

func TestLoadTemplatesFromDirs(t *testing.T) {
	testFs := fakeFs{
		readFileContent: []byte("file-content"),
		dirs:            createTestDirStructure("/templates/path"),
	}
	// add bad files to testFs
	testFs.dirs["/templates/path/..bad-dir-name"] = []fs.FileInfo{
		&fakeFile{name: "bad-no-good-template.jsonnet"},
	}

	manager := filesystemTemplatesManager{
		logger:     zaptest.NewLogger(t).Sugar(),
		fs:         &testFs,
		cacheMutex: &sync.RWMutex{},
		templatePaths: []string{
			"/templates/path",
		},
	}

	templates, err := manager.reloadTemplates()

	sort.Strings(testFs.dirsRead)
	sort.Strings(testFs.filesRead)

	assert.Nil(t, err)
	assert.Equal(
		t,
		[]string{"/templates/path", "/templates/path/default", "/templates/path/default/redis",
			"/templates/path/unified-engagement", "/templates/path/unified-engagement/rate-limiting-service"},
		testFs.dirsRead)
	assert.Equal(
		t,
		[]string{
			"/templates/path/default/redis/destinationrule.jsonnet",
			"/templates/path/default/redis/virtualservice.jsonnet",
			"/templates/path/unified-engagement/rate-limiting-service/rate-limiting-service.jsonnet",
		},
		testFs.filesRead)
	assert.Equal(
		t,
		map[string]string{
			"default_redis_destinationrule":                                  "file-content",
			"default_redis_virtualservice":                                   "file-content",
			"unified-engagement_rate-limiting-service_rate-limiting-service": "file-content",
		},
		templates)

}

func TestLoadTemplatesFromFilesAndDirs(t *testing.T) {
	testFs := fakeFs{
		readFileContent: []byte("file-content"),
		dirs: map[string][]fs.FileInfo{
			"/templates/path": {
				&fakeFile{name: "default_default_virtualservice.jsonnet"},
				&fakeFile{name: "default_default_destinationrule.jsonnet"},
			},
		},
	}
	for key, val := range createTestDirStructure("/templates/dir/path") {
		testFs.dirs[key] = val
	}

	manager := filesystemTemplatesManager{
		logger:     zaptest.NewLogger(t).Sugar(),
		fs:         &testFs,
		cacheMutex: &sync.RWMutex{},
		templatePaths: []string{
			"/templates/path",
			"/templates/dir/path",
		},
	}

	templates, err := manager.reloadTemplates()
	sort.Strings(testFs.dirsRead)
	sort.Strings(testFs.filesRead)

	assert.Nil(t, err)
	assert.Equal(
		t,
		[]string{
			"/templates/dir/path",
			"/templates/dir/path/default",
			"/templates/dir/path/default/redis",
			"/templates/dir/path/unified-engagement",
			"/templates/dir/path/unified-engagement/rate-limiting-service",
			"/templates/path"},
		testFs.dirsRead)
	assert.Equal(
		t,
		[]string{
			"/templates/dir/path/default/redis/destinationrule.jsonnet",
			"/templates/dir/path/default/redis/virtualservice.jsonnet",
			"/templates/dir/path/unified-engagement/rate-limiting-service/rate-limiting-service.jsonnet",
			"/templates/path/default_default_destinationrule.jsonnet",
			"/templates/path/default_default_virtualservice.jsonnet",
		},
		testFs.filesRead)
	assert.Equal(
		t,
		map[string]string{
			"default_default_destinationrule":                                "file-content",
			"default_default_virtualservice":                                 "file-content",
			"default_redis_destinationrule":                                  "file-content",
			"default_redis_virtualservice":                                   "file-content",
			"unified-engagement_rate-limiting-service_rate-limiting-service": "file-content",
		},
		templates)
}

func TestLoadTemplatesFromSymlinkDirs(t *testing.T) {
	// cd dir-style-templates/some-namespace; ln -s ../default/redis-templates some-service-symlinked
	templatePath := "../testdata/dir-style-templates"

	testFs := &testOs{os: &Os{}, filesRead: []string{}, dirsRead: []string{}}
	manager := filesystemTemplatesManager{
		logger:     zaptest.NewLogger(t).Sugar(),
		fs:         testFs,
		cacheMutex: &sync.RWMutex{},
		templatePaths: []string{
			templatePath,
		},
	}

	templates, err := manager.reloadTemplates()
	sort.Strings(testFs.dirsRead)
	sort.Strings(testFs.filesRead)

	assert.Nil(t, err)

	assert.Equal(
		t,
		[]string{
			templatePath,
			path.Join(templatePath, "default"),
			path.Join(templatePath, "default", "redis-templates"),
			path.Join(templatePath, "some-namespace"),
			path.Join(templatePath, "some-namespace", "some-service-symlinked"),
		},
		testFs.dirsRead)

	assert.Equal(
		t,
		[]string{
			path.Join(templatePath, "default", "redis-templates", "destinationrule.jsonnet"),
			path.Join(templatePath, "default", "redis-templates", "virtualservice.jsonnet"),
			path.Join(templatePath, "some-namespace", "some-service-symlinked", "destinationrule.jsonnet"),
			path.Join(templatePath, "some-namespace", "some-service-symlinked", "virtualservice.jsonnet"),
		},
		testFs.filesRead)

	assert.Equal(
		t,
		map[string]string{
			"default_redis-templates_destinationrule":               "\"test destination rule from redis-templates\"",
			"default_redis-templates_virtualservice":                "\"test virtual service from redis-templates\"",
			"some-namespace_some-service-symlinked_destinationrule": "\"test destination rule from redis-templates\"",
			"some-namespace_some-service-symlinked_virtualservice":  "\"test virtual service from redis-templates\"",
		},
		templates)
}

func TestReadTemplateFromFile(t *testing.T) {
	testCases := []struct {
		name                string
		fileName            string
		content             string
		expectedTemplateKey string
		readFileError       string
	}{
		{
			name:                "NormalTemplate",
			fileName:            "other-ns_other-svc_virtualservice.jsonnet",
			content:             "other-vs-content",
			expectedTemplateKey: "other-ns_other-svc_virtualservice",
		},
		{
			name:                "LibTemplate",
			fileName:            "lib_metadata.jsonnet",
			content:             "lib-metadata",
			expectedTemplateKey: "lib_metadata",
		},
		{
			name:          "FailedToRead",
			fileName:      "bad.file",
			readFileError: "Failed to read file!!!",
		},
	}

	for _, tc := range testCases {
		testFs := fakeFs{
			readFileContent: []byte(tc.content),
			error:           tc.readFileError,
		}
		file := fakeFile{name: tc.fileName, dir: false}
		manager := filesystemTemplatesManager{
			logger: zaptest.NewLogger(t).Sugar(),
			fs:     &testFs,
		}

		content, err := manager.readTemplateFile("mount/path", file.Name())
		templateKey := manager.getTemplateKeyForFile(file.Name())

		if tc.readFileError != "" {
			assert.NotNil(t, err, "Expected read file error")
			assert.Equal(t, tc.readFileError, err.Error())
		} else {
			assert.Nil(t, err)
			assert.Equal(t, tc.expectedTemplateKey, templateKey)
			assert.Equal(t, tc.content, content)
		}
	}
}

func TestGetTemplateMetadata(t *testing.T) {
	testCases := []struct {
		name           string
		templateKey    string
		metadata       map[string]*TemplateMetadata
		expectedResult *TemplateMetadata
	}{
		{
			name:        "MetadataExists",
			templateKey: "default/bg-stateless",
			metadata: map[string]*TemplateMetadata{
				"default_bg-stateless": {
					Rollout: &RolloutMetadata{
						MutationTemplate:   "blueGreen",
						ReMutationTemplate: "reMutateBlueGreen",
					},
				},
			},
			expectedResult: &TemplateMetadata{
				Rollout: &RolloutMetadata{
					MutationTemplate:   "blueGreen",
					ReMutationTemplate: "reMutateBlueGreen",
				},
			},
		},
		{
			name:           "MetadataDoesNotExist",
			templateKey:    "unknown/template",
			metadata:       map[string]*TemplateMetadata{},
			expectedResult: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := filesystemTemplatesManager{
				logger:           zaptest.NewLogger(t).Sugar(),
				templateMetadata: tc.metadata,
				cacheMutex:       &sync.RWMutex{},
			}
			result := manager.GetTemplateMetadata(tc.templateKey)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

func TestLoadMetadataFromDirs(t *testing.T) {

	originalValue := features.EnableTemplateMetadata
	features.EnableTemplateMetadata = true
	defer func() { features.EnableTemplateMetadata = originalValue }()

	metadataContent := `rollout:
  mutationTemplate: "blueGreen"
  reMutationTemplate: "reMutateBlueGreen"
`
	testFs := fakeFs{
		readFileContent: []byte(metadataContent),
		dirs: map[string][]fs.FileInfo{
			"/templates/path": {
				&fakeFile{name: "default", dir: true},
			},
			"/templates/path/default": {
				&fakeFile{name: "bg-stateless", dir: true},
			},
			"/templates/path/default/bg-stateless": {
				&fakeFile{name: "virtualservice.jsonnet"},
				&fakeFile{name: "metadata.yaml"},
			},
		},
	}

	manager := filesystemTemplatesManager{
		logger:     zaptest.NewLogger(t).Sugar(),
		fs:         &testFs,
		cacheMutex: &sync.RWMutex{},
		templatePaths: []string{
			"/templates/path",
		},
	}

	_, err := manager.reloadTemplates()
	assert.Nil(t, err)

	metadata := manager.GetTemplateMetadata("default/bg-stateless")
	assert.NotNil(t, metadata)
	assert.NotNil(t, metadata.Rollout)
	assert.Equal(t, "blueGreen", metadata.Rollout.MutationTemplate)
	assert.Equal(t, "reMutateBlueGreen", metadata.Rollout.ReMutationTemplate)
}

func TestLoadMetadataFromFiles(t *testing.T) {
	originalValue := features.EnableTemplateMetadata
	features.EnableTemplateMetadata = true
	defer func() { features.EnableTemplateMetadata = originalValue }()

	metadataContent := `rollout:
  mutationTemplate: "blueGreen"
  reMutationTemplate: "reMutateBlueGreen"
`
	testFs := fakeFs{
		readFileContent: []byte(metadataContent),
		dirs: map[string][]fs.FileInfo{
			"/templates/custom": {
				&fakeFile{name: "default_bg-stateless_virtualservice.jsonnet"},
				&fakeFile{name: "default_bg-stateless_metadata.yaml"},
			},
		},
	}

	manager := filesystemTemplatesManager{
		logger:     zaptest.NewLogger(t).Sugar(),
		fs:         &testFs,
		cacheMutex: &sync.RWMutex{},
		templatePaths: []string{
			"/templates/custom",
		},
	}

	_, err := manager.reloadTemplates()
	assert.Nil(t, err)

	metadata := manager.GetTemplateMetadata("default/bg-stateless")
	assert.NotNil(t, metadata)
	assert.NotNil(t, metadata.Rollout)
	assert.Equal(t, "blueGreen", metadata.Rollout.MutationTemplate)
	assert.Equal(t, "reMutateBlueGreen", metadata.Rollout.ReMutationTemplate)
}

func TestTemplatesManagerIsTemplate(t *testing.T) {
	testCases := []struct {
		name     string
		fileName string
		isDir    bool
		expected bool
	}{
		{
			name:     "NonTemplateFile",
			fileName: "non_template.file",
			expected: false,
		},
		{
			name:     "Directory",
			fileName: "my_directory.jsonnet",
			isDir:    true,
			expected: false,
		},
		{
			name:     "TemplateFile",
			fileName: "my_template.jsonnet",
			expected: true,
		},
	}

	manager := filesystemTemplatesManager{logger: zaptest.NewLogger(t).Sugar(), templatePaths: []string{}}
	for _, tc := range testCases {
		file := fakeFile{name: tc.fileName, dir: tc.isDir}
		actual := manager.isTemplateFile(file)
		assert.Equal(t, tc.expected, actual, tc.name)
	}
}

// Testable fs.FileInfo
type fakeFile struct {
	name string
	dir  bool
}

func (f fakeFile) Name() string       { return f.name }
func (f fakeFile) Size() int64        { return 1 }
func (f fakeFile) Mode() fs.FileMode  { return 066 }
func (f fakeFile) ModTime() time.Time { return time.Now() }
func (f fakeFile) IsDir() bool        { return f.dir }
func (f fakeFile) Sys() interface{}   { return false }

// Testable templating.Fs
type fakeFs struct {
	readFileContent []byte
	error           string
	dirs            map[string][]fs.FileInfo
	filesRead       []string
	dirsRead        []string
}

func (o *fakeFs) ReadFile(filename string) ([]byte, error) {
	if o.error != "" {
		return nil, errors.New(o.error)
	}

	o.filesRead = append(o.filesRead, filename)
	return o.readFileContent, nil
}

func (o *fakeFs) ReadDir(dirname string) ([]fs.FileInfo, error) {
	if o.error != "" {
		return nil, errors.New(o.error)
	}

	o.dirsRead = append(o.dirsRead, dirname)
	return o.dirs[dirname], nil
}

func createTestDirStructure(templatePath string) map[string][]fs.FileInfo {
	templatePath = strings.TrimSuffix(templatePath, TemplateNameSeparator)
	dirs := make(map[string][]fs.FileInfo)

	dirs[templatePath] = []fs.FileInfo{
		&fakeFile{name: "default", dir: true},
		&fakeFile{name: "unified-engagement", dir: true},
	}

	defaultPath := templatePath + TemplateNameSeparator + "default"
	uePath := templatePath + TemplateNameSeparator + "unified-engagement"

	dirs[defaultPath] = []fs.FileInfo{
		&fakeFile{name: "redis", dir: true},
	}
	dirs[uePath] = []fs.FileInfo{
		&fakeFile{name: "rate-limiting-service", dir: true},
	}

	dirs[defaultPath+TemplateNameSeparator+"redis"] = []fs.FileInfo{
		&fakeFile{name: "virtualservice.jsonnet"},
		&fakeFile{name: "destinationrule.jsonnet"},
	}

	dirs[uePath+TemplateNameSeparator+"rate-limiting-service"] = []fs.FileInfo{
		&fakeFile{name: "rate-limiting-service.jsonnet"},
	}

	return dirs
}

// testOs is a testable wrapper around templating.Os
type testOs struct {
	os        *Os
	filesRead []string
	dirsRead  []string
}

func (o *testOs) ReadFile(filename string) ([]byte, error) {
	result, err := o.os.ReadFile(filename)
	if err != nil {
		return result, err
	}
	o.filesRead = append(o.filesRead, filename)
	return result, err
}

func (o *testOs) ReadDir(dirname string) ([]fs.FileInfo, error) {
	result, err := o.os.ReadDir(dirname)
	if err != nil {
		return result, err
	}
	o.dirsRead = append(o.dirsRead, dirname)
	return result, err
}
