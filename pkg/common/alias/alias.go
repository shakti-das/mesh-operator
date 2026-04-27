package alias

import "strings"

var (
	Manager *AliasManager
)

type AliasManager struct {
	labelAliases      map[string][]string
	annotationAliases map[string][]string
}

func NewAliasManager(label map[string]string, annotation map[string]string) *AliasManager {
	newL := parseAlias(label)
	newA := parseAlias(annotation)
	return &AliasManager{
		labelAliases:      newL,
		annotationAliases: newA,
	}
}

func parseAlias(aliasMap map[string]string) map[string][]string {
	newMap := make(map[string][]string)
	for k, v := range aliasMap {
		newMap[k] = strings.Split(v, ":")
	}
	return newMap
}

func (m *AliasManager) GetLabelAliases() map[string][]string {
	if m == nil {
		return nil
	}
	return m.labelAliases
}

func (m *AliasManager) GetAnnotationAliases() map[string][]string {
	if m == nil {
		return nil
	}
	return m.annotationAliases
}
