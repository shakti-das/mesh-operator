package templating

import (
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"github.com/fsnotify/fsnotify"

	"go.uber.org/zap"
)

const (
	TemplateFileSuffix        = ".jsonnet"
	TemplateKeyDelimiter      = constants.TemplateKeyDelimiter
	TemplatesLibDirectoryName = "lib"
	MetadataFileName          = "metadata.yaml"

	RescanDelaySeconds = 1
)

type TemplatesManager interface {
	GetTemplatesByPrefix(prefix string) map[string]string
	Exists(prefix string) bool
	GetTemplatePaths() []string
	GetTemplateMetadata(templateKey string) *TemplateMetadata
}

type filesystemTemplatesManager struct {
	logger           *zap.SugaredLogger
	templatePaths    []string
	templates        map[string]string
	templateMetadata map[string]*TemplateMetadata
	fs               Fs
	cacheMutex       *sync.RWMutex
}

func NewTemplatesManagerOrDie(logger *zap.SugaredLogger, stopCh <-chan struct{}, templatePaths []string) TemplatesManager {
	templateManager, err := NewTemplatesManager(logger, stopCh, templatePaths)
	if err != nil {
		logger.Fatalw("Can't create templates manager", "error", err)
	}
	return templateManager
}

func NewTemplatesManager(logger *zap.SugaredLogger, stop <-chan struct{}, templatePaths []string) (TemplatesManager, error) {

	manager := filesystemTemplatesManager{
		logger:        logger,
		templatePaths: templatePaths,
		fs:            &Os{},
		cacheMutex:    &sync.RWMutex{}}

	_, err := manager.reloadTemplates()
	if err != nil {
		return nil, err
	}
	if err = manager.StartWatcher(stop); err != nil {
		return nil, err
	}
	return &manager, nil
}

func (m *filesystemTemplatesManager) GetTemplatePaths() []string {
	return m.templatePaths
}

func (m *filesystemTemplatesManager) GetTemplatesByPrefix(prefix string) map[string]string {
	m.cacheMutex.RLock()
	defer m.cacheMutex.RUnlock()
	var foundTemplates = make(map[string]string)
	for key, template := range m.templates {
		if strings.HasPrefix(key, prefix) {
			foundTemplates[key] = template
		}
	}
	return foundTemplates
}

func (m *filesystemTemplatesManager) Exists(prefix string) bool {
	return len(m.GetTemplatesByPrefix(prefix)) > 0
}

// GetTemplateMetadata returns the metadata for a template identified by templateKey (e.g., "default/bg-stateless").
func (m *filesystemTemplatesManager) GetTemplateMetadata(templateKey string) *TemplateMetadata {
	m.cacheMutex.RLock()
	defer m.cacheMutex.RUnlock()
	internalKey := strings.ReplaceAll(templateKey, constants.TemplateNameSeparator, TemplateKeyDelimiter)
	return m.templateMetadata[internalKey]
}

func (m *filesystemTemplatesManager) reloadTemplates() (map[string]string, error) {
	m.logger.Debugf("Reloading templates for template paths %v", m.templatePaths)
	loadedTemplates := make(map[string]string)
	loadedMetadata := make(map[string]*TemplateMetadata)

	for _, templatesPath := range m.templatePaths {
		templateFiles, err := m.fs.ReadDir(templatesPath)
		if err != nil {
			return nil, err
		}
		for _, f := range templateFiles {
			if m.isTemplateFile(f) {
				err = m.loadTemplateForFile(templatesPath, f, loadedTemplates)
				if err != nil {
					return nil, err
				}
			} else if m.isTemplateDir(f, templatesPath) {
				if f.Name() != TemplatesLibDirectoryName {
					err = m.handleDirectoryTemplates(templatesPath, f, loadedTemplates, loadedMetadata)
					if err != nil {
						return nil, err
					}
				}
			} else if features.EnableTemplateMetadata && m.isMetadataFile(f) {
				err = m.loadMetadataForFile(templatesPath, f, loadedMetadata)
				if err != nil {
					return nil, err
				}
			} else {
				m.logger.Warnf("Invalid file present in the templates path: %s", f.Name())
			}
		}
	}
	if features.EnableTemplateMetadata {
		m.logger.Infof("Loaded %d templates and %d template metadata files.", len(loadedTemplates), len(loadedMetadata))
		m.replaceTemplatesAndMetadata(loadedTemplates, loadedMetadata)
	} else {
		m.logger.Infof("Loaded %d templates.", len(loadedTemplates))
		m.replaceTemplates(loadedTemplates)
	}
	return loadedTemplates, nil
}

// handleDirectoryTemplates loads templates residing in dir structure <namespace>/<app>/<resource.jsonnet>, <folder>/<resource.jsonnet>
// (e.g. $TEMPLATEPATH/namespace/service/destinationrule.jsonnet)
// and loads the template contents in the provided map.
// It also loads metadata.yaml files from template directories into loadedMetadata.
func (m *filesystemTemplatesManager) handleDirectoryTemplates(basePath string, namespaceDir fs.FileInfo,
	loadedTemplates map[string]string, loadedMetadata map[string]*TemplateMetadata) error {
	m.logger.Debugf("Loading templates from a directory structure from namespace dir %s on path %s",
		namespaceDir.Name(), basePath)

	namespaceDirPath := path.Join(basePath, namespaceDir.Name())
	apps, err := m.fs.ReadDir(namespaceDirPath)
	if err != nil {
		m.logger.Errorf("Cannot read template ns directory %s", namespaceDirPath)
		return err
	}
	for _, app := range apps {
		if m.isTemplateDir(app, namespaceDirPath) {
			m.logger.Debugf("Loading templates from a directory structure from app dir %s on path %s",
				app.Name(), namespaceDirPath)

			appDirPath := path.Join(namespaceDirPath, app.Name())
			templateFiles, err := m.fs.ReadDir(appDirPath)
			if err != nil {
				m.logger.Errorf("Cannot read template directory %s", appDirPath)
				return err
			}
			for _, templateFile := range templateFiles {
				if m.isTemplateFile(templateFile) {
					err = m.loadTemplateForDir(namespaceDir.Name(), app.Name(),
						appDirPath, templateFile.Name(), loadedTemplates)
					if err != nil {
						m.logger.Errorf("Could not load template from file %s/%s", appDirPath,
							templateFile.Name())
						return err
					}
				} else if features.EnableTemplateMetadata && m.isMetadataFile(templateFile) {
					err = m.loadMetadataForDir(namespaceDir.Name(), app.Name(), appDirPath, loadedMetadata)
					if err != nil {
						m.logger.Errorf("Could not load metadata from file %s/%s", appDirPath, MetadataFileName)
						return err
					}
				}
			}
		} else {
			fileName := app.Name()
			templateKey := namespaceDir.Name() + TemplateKeyDelimiter + fileName[0:len(fileName)-len(TemplateFileSuffix)]
			err = m.loadTemplate(templateKey, namespaceDirPath, app.Name(), loadedTemplates)
			if err != nil {
				m.logger.Errorf("Could not load template from file %s/%s", namespaceDirPath, fileName)
				return err
			}
		}
	}
	return nil
}

func (m *filesystemTemplatesManager) loadTemplateForFile(templatesPath string, f fs.FileInfo,
	loadedTemplates map[string]string) error {
	m.logger.Debugf("Loading file-based template for %s in %s", f.Name(), templatesPath)
	templateKey := m.getTemplateKeyForFile(f.Name())
	return m.loadTemplate(templateKey, templatesPath, f.Name(), loadedTemplates)
}

func (m *filesystemTemplatesManager) loadTemplateForDir(namespace string, app string, appDirPath string, fileName string,
	loadedTemplates map[string]string) error {
	m.logger.Debugf("Loading directory-based template for %s in %s", fileName, appDirPath)
	templateKey := m.getTemplateKeyForDir(namespace, app, fileName)
	return m.loadTemplate(templateKey, appDirPath, fileName, loadedTemplates)
}

func (m *filesystemTemplatesManager) loadTemplate(templateKey string, pathToTemplate string, fileName string,
	loadedTemplates map[string]string) error {
	template, err := m.readTemplateFile(pathToTemplate, fileName)
	if err != nil {
		return err
	}
	loadedTemplates[templateKey] = template
	m.logger.Infof("Loaded template %s => %s", templateKey, fileName)
	return nil
}

func (m *filesystemTemplatesManager) replaceTemplates(newTemplates map[string]string) {
	m.cacheMutex.Lock()
	m.templates = newTemplates
	m.cacheMutex.Unlock()
	m.logger.Debug("Replaced templates successfully")
}

func (m *filesystemTemplatesManager) replaceTemplatesAndMetadata(newTemplates map[string]string, newMetadata map[string]*TemplateMetadata) {
	m.cacheMutex.Lock()
	m.templates = newTemplates
	m.templateMetadata = newMetadata
	m.cacheMutex.Unlock()
	m.logger.Debug("Replaced templates and metadata successfully")
}

// isMetadataFile returns true for any metadata file, whether flat (e.g. "core-on-sam_coreapp-argo-bg_metadata.yaml")
// or directory-based (e.g. "namespace/app/metadata.yaml").
func (m *filesystemTemplatesManager) isMetadataFile(f fs.FileInfo) bool {
	return !f.IsDir() && strings.HasSuffix(f.Name(), MetadataFileName)
}

func (m *filesystemTemplatesManager) loadMetadataForFile(templatesPath string, f fs.FileInfo,
	loadedMetadata map[string]*TemplateMetadata,
) error {
	fileName := f.Name()
	suffix := TemplateKeyDelimiter + MetadataFileName
	metadataKey := fileName[:len(fileName)-len(suffix)]
	return m.loadMetadata(metadataKey, templatesPath, fileName, loadedMetadata)
}

func (m *filesystemTemplatesManager) loadMetadataForDir(namespace string, app string, appDirPath string,
	loadedMetadata map[string]*TemplateMetadata,
) error {
	metadataKey := namespace + TemplateKeyDelimiter + app
	return m.loadMetadata(metadataKey, appDirPath, MetadataFileName, loadedMetadata)
}

func (m *filesystemTemplatesManager) loadMetadata(metadataKey string, basePath string, fileName string,
	loadedMetadata map[string]*TemplateMetadata,
) error {
	templateMetadata, err := m.readMetadataFile(basePath, fileName)
	if err != nil {
		return err
	}
	loadedMetadata[metadataKey] = templateMetadata
	m.logger.Infof("Loaded metadata %s => %s", metadataKey, fileName)
	return nil
}

func (m *filesystemTemplatesManager) readMetadataFile(basePath string, fileName string) (*TemplateMetadata, error) {
	fullPath := path.Join(basePath, fileName)
	m.logger.Debugf("Reading metadata file %s", fullPath)
	content, err := m.fs.ReadFile(fullPath)
	if err != nil {
		return nil, err
	}
	return ParseTemplateMetadata(content)
}

func (m *filesystemTemplatesManager) hasValidName(f fs.FileInfo) bool {
	first := f.Name()[0]
	if first < 'a' || first > 'z' {
		m.logger.Debugf("Found file/dir with an invalid name: %s", f.Name())
		return false
	}
	return true
}

func (m *filesystemTemplatesManager) isTemplateFile(f fs.FileInfo) bool {
	return !f.IsDir() && m.hasValidName(f) &&
		strings.HasSuffix(f.Name(), TemplateFileSuffix)
}

// isTemplateDir returns true for a directory or a symlink pointing to a directory
func (m *filesystemTemplatesManager) isTemplateDir(f fs.FileInfo, basePath string) bool {
	return (f.IsDir() || m.isTemplateDirSymlink(f, basePath)) && m.hasValidName(f)
}

func (m *filesystemTemplatesManager) isTemplateDirSymlink(f fs.FileInfo, basePath string) bool {
	if f.Mode()&fs.ModeSymlink != 0 {
		linked, err := os.Stat(path.Join(basePath, f.Name()))
		if err != nil {
			m.logger.Errorf("Cannot find file info for the symlink %s/%s", basePath, f.Name())
			return false
		}
		return linked.IsDir()
	}
	return false
}

func (m *filesystemTemplatesManager) readTemplateFile(basePath string, fileName string) (string, error) {
	fullPath := path.Join(basePath, fileName)
	m.logger.Debugf("Reading template file %s", fullPath)
	template, err := m.fs.ReadFile(fullPath)
	if err != nil {
		return "", err
	}

	return string(template), nil
}

func (m *filesystemTemplatesManager) getTemplateKeyForFile(fileName string) string {
	return fileName[0 : len(fileName)-len(TemplateFileSuffix)]
}

func (m *filesystemTemplatesManager) getTemplateKeyForDir(namespace string, app string, fileName string) string {
	return namespace + TemplateKeyDelimiter + app + TemplateKeyDelimiter +
		fileName[0:len(fileName)-len(TemplateFileSuffix)]
}

// StartWatcher
// Starts a new thread that's watching file-system changes
// When k8s remounts volume mount, a lot of events occur such as file addition/removal etc.
// Because of this we delay the templates rescan for the duration of the RescanDelaySeconds after the very last file-system event received.
func (m *filesystemTemplatesManager) StartWatcher(stop <-chan struct{}) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	for _, templatesPath := range m.templatePaths {
		if err := watcher.Add(templatesPath); err != nil {
			return err
		}
	}

	go func() {
		defer watcher.Close()

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		var rescanAfter time.Time
		accumulated := 0
		for {
			select {
			case e := <-watcher.Events:
				if e.Op == fsnotify.Chmod {
					break
				}
				accumulated += 1
				rescanAfter = time.Now().Add(time.Second * RescanDelaySeconds)
				m.logger.Debugf("FS event recorded: %s/%s accumulated %d events", e.Name, e.Op.String(), accumulated)
			case err := <-watcher.Errors:
				if err != nil {
					m.logger.Error("File watcher error:", err)
				}
			case <-ticker.C:
				if accumulated > 0 && time.Now().After(rescanAfter) {
					accumulated = 0
					m.logger.Debugf("Triggering templates reload. Accumulated %d events", accumulated)
					_, loadError := m.reloadTemplates()
					if loadError != nil {
						m.logger.Error("Failed to reload templates", loadError)
					}
				}
			case <-stop:
				m.logger.Info("Stopping file watcher.")
				return
			}
		}
	}()

	return nil
}

// Fs an abstraction for the file system to enable testing
type Fs interface {
	ReadFile(filename string) ([]byte, error)
	ReadDir(dirname string) ([]fs.FileInfo, error)
}

// Os an OS implementation of the file-system abstraction
type Os struct{}

func (o *Os) ReadFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filename)
}

func (o *Os) ReadDir(dirname string) ([]fs.FileInfo, error) {
	return ioutil.ReadDir(dirname)
}
