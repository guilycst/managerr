package observe

import (
	"fmt"
	"path"
	"strings"

	"github.com/guilycst/mastarr/internal/domain"
)

// MapExternalPath translates one upstream namespace path into a configured
// root-relative target. SourcePrefix is the upstream prefix; DestinationPrefix
// is the root-relative prefix. Matching is component-aware.
func MapExternalPath(mapping domain.PathMapping, externalPath string) (domain.FileTarget, error) {
	if err := validateMapping(mapping); err != nil {
		return domain.FileTarget{}, err
	}
	external, err := cleanNamespacePath(externalPath)
	if err != nil {
		return domain.FileTarget{}, err
	}
	source, err := cleanSourcePrefix(mapping.SourcePrefix)
	if err != nil {
		return domain.FileTarget{}, err
	}
	destination, err := cleanDestinationPrefix(mapping.DestinationPrefix)
	if err != nil {
		return domain.FileTarget{}, err
	}
	if !namespaceHasPrefix(external, source) {
		return domain.FileTarget{}, fmt.Errorf("%w: %q", ErrMappingMismatch, externalPath)
	}
	suffix := strings.TrimPrefix(external, source)
	suffix = strings.TrimPrefix(suffix, "/")
	relative := destination
	if suffix != "" {
		if relative != "" {
			relative += "/"
		}
		relative += suffix
	}
	target := domain.FileTarget{RootID: mapping.RootID, RelativePath: relative}
	if err := target.Validate(); err != nil {
		if relative == "" {
			return domain.FileTarget{}, fmt.Errorf("%w: mapped path resolves to root", ErrRootTarget)
		}
		return domain.FileTarget{}, err
	}
	return target, nil
}

// MapRootPath translates a configured root-relative target into the upstream
// namespace represented by mapping. It is useful when read-back paths arrive
// from a manager in its own namespace.
func MapRootPath(mapping domain.PathMapping, target domain.FileTarget) (string, error) {
	if err := validateMapping(mapping); err != nil {
		return "", err
	}
	if err := target.Validate(); err != nil {
		return "", err
	}
	if target.RootID != mapping.RootID {
		return "", fmt.Errorf("%w: root %q is not mapped by %q", ErrMappingMismatch, target.RootID, mapping.ID)
	}
	destination, err := cleanDestinationPrefix(mapping.DestinationPrefix)
	if err != nil {
		return "", err
	}
	source, err := cleanSourcePrefix(mapping.SourcePrefix)
	if err != nil {
		return "", err
	}
	if !namespaceHasPrefix(target.RelativePath, destination) {
		return "", fmt.Errorf("%w: %q", ErrMappingMismatch, target.RelativePath)
	}
	suffix := strings.TrimPrefix(target.RelativePath, destination)
	suffix = strings.TrimPrefix(suffix, "/")
	result := source
	if suffix != "" {
		if result == "" || result == "/" {
			result = strings.TrimRight(result, "/") + "/" + suffix
		} else {
			result += "/" + suffix
		}
	}
	if result == "" {
		return "", fmt.Errorf("%w: mapping has no source path", ErrMappingMismatch)
	}
	return result, nil
}

// SelectMapping chooses the unique most-specific mapping for an upstream path.
// Equal-specificity matches are rejected, including mappings targeting the
// same root, because an approval must name one authority-bearing mapping.
func SelectMapping(mappings []domain.PathMapping, connectionID domain.ConfigID, externalPath string) (domain.PathMapping, error) {
	if !connectionID.Valid() {
		return domain.PathMapping{}, fmt.Errorf("%w: invalid connection id", ErrMappingMismatch)
	}
	external, err := cleanNamespacePath(externalPath)
	if err != nil {
		return domain.PathMapping{}, err
	}
	var selected domain.PathMapping
	selectedLength := -1
	for _, mapping := range mappings {
		if mapping.ConnectionID != connectionID {
			continue
		}
		if err := validateMapping(mapping); err != nil {
			return domain.PathMapping{}, err
		}
		source, err := cleanSourcePrefix(mapping.SourcePrefix)
		if err != nil {
			return domain.PathMapping{}, err
		}
		if !namespaceHasPrefix(external, source) {
			continue
		}
		length := namespaceComponentCount(source)
		if length < selectedLength {
			continue
		}
		if length == selectedLength {
			return domain.PathMapping{}, fmt.Errorf("%w: %q", ErrAmbiguousMapping, externalPath)
		}
		selected, selectedLength = mapping, length
	}
	if selectedLength < 0 {
		return domain.PathMapping{}, fmt.Errorf("%w: %q", ErrMappingMismatch, externalPath)
	}
	return selected, nil
}

// MapExternalPathForConnection selects and translates one mapping in one
// connection scope.
func MapExternalPathForConnection(mappings []domain.PathMapping, connectionID domain.ConfigID, externalPath string) (domain.FileTarget, error) {
	mapping, err := SelectMapping(mappings, connectionID, externalPath)
	if err != nil {
		return domain.FileTarget{}, err
	}
	return MapExternalPath(mapping, externalPath)
}

func validateMapping(mapping domain.PathMapping) error {
	if !mapping.ID.Valid() || !mapping.ConnectionID.Valid() || !mapping.RootID.Valid() {
		return fmt.Errorf("%w: mapping references are invalid", ErrMappingMismatch)
	}
	return nil
}

func cleanNamespacePath(value string) (string, error) {
	if value == "" || strings.IndexByte(value, 0) >= 0 || strings.Contains(value, "\\") {
		return "", fmt.Errorf("%w: namespace path is invalid", ErrMappingMismatch)
	}
	abs := strings.HasPrefix(value, "/")
	clean := path.Clean(value)
	if clean == "." || clean != value {
		return "", fmt.Errorf("%w: namespace path is not canonical", ErrMappingMismatch)
	}
	if clean == "/" {
		return clean, nil
	}
	if !abs {
		if err := domain.ValidateRelativePath(clean); err != nil {
			return "", fmt.Errorf("%w: %v", ErrPathEscape, err)
		}
	} else {
		for _, part := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
			if part == "" || part == "." || part == ".." {
				return "", fmt.Errorf("%w: namespace path contains unsafe component", ErrPathEscape)
			}
		}
	}
	return clean, nil
}

func cleanSourcePrefix(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return cleanNamespacePath(value)
}

func cleanDestinationPrefix(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%w: destination prefix must be root-relative", ErrPathEscape)
	}
	return cleanNamespacePath(value)
}

func namespaceHasPrefix(value, prefix string) bool {
	if prefix == "" {
		return true
	}
	if prefix == "/" {
		return strings.HasPrefix(value, "/")
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func namespaceComponentCount(value string) int {
	value = strings.Trim(value, "/")
	if value == "" {
		return 0
	}
	return len(strings.Split(value, "/"))
}
