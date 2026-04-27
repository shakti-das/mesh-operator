package cmd

import (
	"os"

	"k8s.io/apimachinery/pkg/util/yaml"

	"go.uber.org/zap"
)

func ReadFileOrDie(logger *zap.SugaredLogger, filePath string, fileInfo string) *os.File {
	if filePath == "" {
		logger.Panicf("No path to resource specified: %s", fileInfo)
	}
	f, err := os.Open(filePath)
	if err != nil {
		logger.Panicf("Failed to open resource file.",
			"path", filePath,
			"resource", fileInfo, "error", err)
	}
	return f
}

func DecodeYamlOrJsonOrDie(logger *zap.SugaredLogger, file *os.File, fileInfo string, object interface{}) {
	decoder := yaml.NewYAMLOrJSONDecoder(file, 1024)
	err := decoder.Decode(object)

	if err != nil {
		logger.Panicf("Failed to unmarshal resource file.",
			"resource", fileInfo,
			"error", err)
	}
}
