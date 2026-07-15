package machinecatalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

func (c *Catalog) ResolveTool(kind, id, version, target, deliveryMode, environment string) (ToolManifest, error) {
	if c == nil || (kind != "generator" && kind != "sdk") {
		return ToolManifest{}, ErrUnknownTool
	}
	versions, exists := c.tools[toolKey(kind, id)]
	if !exists {
		return ToolManifest{}, fmt.Errorf("%w: %s %s@%s", ErrUnknownTool, kind, id, version)
	}
	for _, manifest := range versions {
		if manifest.Version != version {
			continue
		}
		if !contains(manifest.SupportedTargets, target) || !contains(manifest.SupportedDeliveryModes, deliveryMode) || !contains(manifest.SupportedEnvironments, environment) {
			return ToolManifest{}, fmt.Errorf("%w: %s %s@%s does not support %s/%s/%s", ErrToolIncompatible, kind, id, version, target, deliveryMode, environment)
		}
		return manifest, nil
	}
	return ToolManifest{}, fmt.Errorf("%w: %s %s@%s", ErrUnknownTool, kind, id, version)
}

func (c *Catalog) Resolve(request ResolveRequest) (Resolution, error) {
	if request.TemplateID == "" {
		return Resolution{}, ErrUnknownTemplate
	}
	templates, exists := c.templates[request.TemplateID]
	if !exists {
		return Resolution{}, fmt.Errorf("%w: %s", ErrUnknownTemplate, request.TemplateID)
	}
	if request.TemplateRange == "" {
		request.TemplateRange = "*"
	}
	constraints := make(map[string][]string)
	for _, requirement := range request.Packages {
		constraints[requirement.PackageID] = append(constraints[requirement.PackageID], requirement.VersionRange)
	}
	var lastErr error
	versionMatched := false
	for _, template := range templates {
		matches, err := versionMatchesAll(template.Version, []string{request.TemplateRange})
		if err != nil {
			return Resolution{}, err
		}
		if !matches {
			continue
		}
		versionMatched = true
		if err := validateSelectionAvailability(template.TemplateID+"@"+template.Version, template.Availability, request); err != nil {
			lastErr = err
			continue
		}
		if !hasEntrypoint(template, request.Target, request.DeliveryMode) {
			lastErr = fmt.Errorf("%w: %s@%s has no %s/%s entrypoint", ErrEntrypointMismatch, template.TemplateID, template.Version, request.Target, request.DeliveryMode)
			continue
		}
		selected, err := c.solve(constraints, map[string]PackageManifest{}, request, template)
		if err != nil {
			lastErr = err
			continue
		}
		packages := selectedPackages(selected)
		snapshot, err := c.snapshot(packages, []TemplateManifest{template})
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Packages: packages, Template: template, Snapshot: snapshot}, nil
	}
	if !versionMatched {
		return Resolution{}, fmt.Errorf("%w: template %s requires %s", ErrVersionConflict, request.TemplateID, request.TemplateRange)
	}
	if lastErr != nil {
		return Resolution{}, lastErr
	}
	return Resolution{}, fmt.Errorf("%w: template %s", ErrTemplateIncompatible, request.TemplateID)
}

func (c *Catalog) Snapshot() (CatalogSnapshot, error) {
	packages := make([]PackageManifest, 0)
	for _, versions := range c.packages {
		packages = append(packages, versions...)
	}
	templates := make([]TemplateManifest, 0)
	for _, versions := range c.templates {
		templates = append(templates, versions...)
	}
	return c.snapshot(packages, templates)
}

func (c *Catalog) solve(constraints map[string][]string, selected map[string]PackageManifest, request ResolveRequest, template TemplateManifest) (map[string]PackageManifest, error) {
	for packageID, ranges := range constraints {
		if manifest, exists := selected[packageID]; exists {
			matches, err := versionMatchesAll(manifest.Version, ranges)
			if err != nil || !matches {
				return nil, fmt.Errorf("%w: %s requires %s", ErrVersionConflict, packageID, strings.Join(ranges, " & "))
			}
		}
	}
	unresolved := make([]string, 0)
	for packageID := range constraints {
		if _, exists := selected[packageID]; !exists {
			unresolved = append(unresolved, packageID)
		}
	}
	if len(unresolved) == 0 {
		packages := selectedPackages(selected)
		if err := validateDependencyCycles(packages); err != nil {
			return nil, err
		}
		if err := validateConflicts(packages); err != nil {
			return nil, err
		}
		if err := validateTemplatePackages(template, packages); err != nil {
			return nil, err
		}
		if err := validatePackageTemplateRequirements(template, packages); err != nil {
			return nil, err
		}
		return selected, nil
	}
	sort.Strings(unresolved)
	packageID := unresolved[0]
	versions, exists := c.packages[packageID]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownPackage, packageID)
	}
	var lastErr error
	versionMatched := false
	for _, candidate := range versions {
		matches, err := versionMatchesAll(candidate.Version, constraints[packageID])
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		versionMatched = true
		if err := validateSelectionAvailability(candidate.PackageID+"@"+candidate.Version, candidate.Availability, request); err != nil {
			lastErr = err
			continue
		}
		if err := validateTemplatePackageCandidate(template, candidate); err != nil {
			lastErr = err
			continue
		}
		nextSelected := cloneSelected(selected)
		nextSelected[packageID] = candidate
		if err := validateConflicts(selectedPackages(nextSelected)); err != nil {
			lastErr = err
			continue
		}
		nextConstraints := cloneConstraints(constraints)
		for _, dependency := range candidate.Dependencies {
			nextConstraints[dependency.PackageID] = append(nextConstraints[dependency.PackageID], dependency.VersionRange)
		}
		resolved, err := c.solve(nextConstraints, nextSelected, request, template)
		if err == nil {
			return resolved, nil
		}
		lastErr = err
	}
	if !versionMatched {
		return nil, fmt.Errorf("%w: %s requires %s", ErrVersionConflict, packageID, strings.Join(constraints[packageID], " & "))
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: %s", ErrVersionConflict, packageID)
}

func selectedPackages(selected map[string]PackageManifest) []PackageManifest {
	packages := make([]PackageManifest, 0, len(selected))
	for _, manifest := range selected {
		packages = append(packages, manifest)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].PackageID < packages[j].PackageID })
	return packages
}

func validateTemplatePackageCandidate(template TemplateManifest, manifest PackageManifest) error {
	if err := validateTemplatePackages(template, []PackageManifest{manifest}); err != nil {
		return err
	}
	return validatePackageTemplateRequirements(template, []PackageManifest{manifest})
}

func validateSelectionAvailability(subject string, availability []Availability, request ResolveRequest) error {
	var targetSeen, modeSeen bool
	for _, entry := range availability {
		if entry.Target == request.Target {
			targetSeen = true
		}
		if entry.DeliveryMode == request.DeliveryMode {
			modeSeen = true
		}
		if entry.Target == request.Target && entry.DeliveryMode == request.DeliveryMode && contains(entry.Environments, request.Environment) {
			return nil
		}
	}
	if !targetSeen {
		return fmt.Errorf("%w: %s/%s", ErrUnsupportedTarget, subject, request.Target)
	}
	if !modeSeen {
		return fmt.Errorf("%w: %s/%s", ErrUnsupportedDeliveryMode, subject, request.DeliveryMode)
	}
	return fmt.Errorf("%w: %s/%s/%s/%s", ErrUnavailableEnvironment, subject, request.Target, request.DeliveryMode, request.Environment)
}

func validateDependencyCycles(packages []PackageManifest) error {
	byID := make(map[string]PackageManifest, len(packages))
	for _, manifest := range packages {
		byID[manifest.PackageID] = manifest
	}
	state := make(map[string]int)
	stack := make([]string, 0)
	var visit func(string) error
	visit = func(packageID string) error {
		if state[packageID] == 1 {
			start := 0
			for index, item := range stack {
				if item == packageID {
					start = index
					break
				}
			}
			cycle := append(append([]string(nil), stack[start:]...), packageID)
			return fmt.Errorf("%w: %s", ErrDependencyCycle, strings.Join(cycle, " -> "))
		}
		if state[packageID] == 2 {
			return nil
		}
		state[packageID] = 1
		stack = append(stack, packageID)
		for _, dependency := range byID[packageID].Dependencies {
			if _, exists := byID[dependency.PackageID]; exists {
				if err := visit(dependency.PackageID); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[packageID] = 2
		return nil
	}
	ids := make([]string, 0, len(byID))
	for packageID := range byID {
		ids = append(ids, packageID)
	}
	sort.Strings(ids)
	for _, packageID := range ids {
		if err := visit(packageID); err != nil {
			return err
		}
	}
	return nil
}

func validateConflicts(packages []PackageManifest) error {
	byID := make(map[string]PackageManifest, len(packages))
	for _, manifest := range packages {
		byID[manifest.PackageID] = manifest
	}
	for _, manifest := range packages {
		for _, conflict := range manifest.Conflicts {
			other, exists := byID[conflict.PackageID]
			if !exists {
				continue
			}
			matches, err := versionMatchesAll(other.Version, []string{conflict.VersionRange})
			if err != nil {
				return err
			}
			if matches {
				return fmt.Errorf("%w: %s@%s conflicts with %s@%s (%s)", ErrPackageConflict, manifest.PackageID, manifest.Version, other.PackageID, other.Version, conflict.VersionRange)
			}
		}
	}
	return nil
}

func validateTemplatePackages(template TemplateManifest, packages []PackageManifest) error {
	compatibility := make(map[string]Requirement, len(template.PackageCompatibility))
	for _, requirement := range template.PackageCompatibility {
		if _, duplicate := compatibility[requirement.PackageID]; duplicate {
			return fmt.Errorf("%w: duplicate compatibility for %s", ErrTemplateIncompatible, requirement.PackageID)
		}
		compatibility[requirement.PackageID] = requirement
	}
	blocks := make(map[string]struct{}, len(template.SupportedBlocks))
	for _, block := range template.SupportedBlocks {
		blocks[block] = struct{}{}
	}
	for _, manifest := range packages {
		requirement, exists := compatibility[manifest.PackageID]
		if !exists {
			return fmt.Errorf("%w: %s does not declare %s", ErrTemplateIncompatible, template.TemplateID, manifest.PackageID)
		}
		matches, err := versionMatchesAll(manifest.Version, []string{requirement.VersionRange})
		if err != nil || !matches {
			return fmt.Errorf("%w: %s requires %s %s, selected %s", ErrTemplateIncompatible, template.TemplateID, manifest.PackageID, requirement.VersionRange, manifest.Version)
		}
		for _, block := range manifest.ClientBlocks {
			if _, exists := blocks[block]; !exists {
				return fmt.Errorf("%w: %s requires %s from %s", ErrTemplateMissingBlock, template.TemplateID, block, manifest.PackageID)
			}
		}
	}
	return nil
}

func validatePackageTemplateRequirements(template TemplateManifest, packages []PackageManifest) error {
	for _, manifest := range packages {
		matched := false
		for _, requirement := range manifest.UITemplateCompatibility {
			if requirement.TemplateID != template.TemplateID {
				continue
			}
			compatible, err := versionMatchesAll(template.Version, []string{requirement.VersionRange})
			if err != nil {
				return err
			}
			if compatible {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: %s@%s does not allow %s@%s", ErrTemplateIncompatible, manifest.PackageID, manifest.Version, template.TemplateID, template.Version)
		}
	}
	return nil
}

func hasEntrypoint(template TemplateManifest, target, mode string) bool {
	for _, entrypoint := range template.Entrypoints {
		if entrypoint.Target == target && entrypoint.DeliveryMode == mode {
			return true
		}
	}
	return false
}

func versionMatchesAll(version string, ranges []string) (bool, error) {
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		return false, err
	}
	for _, expression := range ranges {
		if expression == "" {
			expression = "*"
		}
		constraint, err := semver.NewConstraint(expression)
		if err != nil {
			return false, err
		}
		if !constraint.Check(parsed) {
			return false, nil
		}
	}
	return true, nil
}

func cloneSelected(source map[string]PackageManifest) map[string]PackageManifest {
	result := make(map[string]PackageManifest, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneConstraints(source map[string][]string) map[string][]string {
	result := make(map[string][]string, len(source))
	for key, value := range source {
		result[key] = append([]string(nil), value...)
	}
	return result
}
