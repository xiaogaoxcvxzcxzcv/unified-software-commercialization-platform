package generation

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type Workspace struct {
	Reference    string
	TargetRoot   string
	ArtifactRoot string
}

type WorkspaceCatalog struct {
	byReference map[string]Workspace
}

func NewWorkspaceCatalog(entries []Workspace) (*WorkspaceCatalog, error) {
	if len(entries) == 0 {
		return nil, ErrInvalidInput
	}
	catalog := &WorkspaceCatalog{byReference: make(map[string]Workspace, len(entries))}
	usedRoots := make(map[string]string, len(entries)*2)
	for _, entry := range entries {
		if !stableIdentifierPattern.MatchString(entry.Reference) {
			return nil, ErrInvalidInput
		}
		targetRoot, err := validateWorkspaceRoot(entry.TargetRoot)
		if err != nil {
			return nil, fmt.Errorf("%w: target root for %s", ErrTargetUnsafe, entry.Reference)
		}
		artifactRoot, err := validateWorkspaceRoot(entry.ArtifactRoot)
		if err != nil {
			return nil, fmt.Errorf("%w: artifact root for %s", ErrTargetUnsafe, entry.Reference)
		}
		if rootsOverlap(targetRoot, artifactRoot) {
			return nil, ErrInvalidInput
		}
		if _, duplicate := catalog.byReference[entry.Reference]; duplicate {
			return nil, ErrInvalidInput
		}
		for _, root := range []string{targetRoot, artifactRoot} {
			key := strings.ToLower(filepath.Clean(root))
			if usedBy := usedRoots[key]; usedBy != "" {
				return nil, fmt.Errorf("%w: workspace root reused by %s and %s", ErrInvalidInput, usedBy, entry.Reference)
			}
			usedRoots[key] = entry.Reference
		}
		catalog.byReference[entry.Reference] = Workspace{Reference: entry.Reference, TargetRoot: targetRoot, ArtifactRoot: artifactRoot}
	}
	return catalog, nil
}

func (c *WorkspaceCatalog) Resolve(reference string) (Workspace, error) {
	if c == nil {
		return Workspace{}, ErrInvalidInput
	}
	workspace, ok := c.byReference[reference]
	if !ok {
		return Workspace{}, ErrInvalidInput
	}
	return workspace, nil
}

func (c *WorkspaceCatalog) References() []string {
	if c == nil {
		return nil
	}
	values := make([]string, 0, len(c.byReference))
	for reference := range c.byReference {
		values = append(values, reference)
	}
	sort.Strings(values)
	return values
}
