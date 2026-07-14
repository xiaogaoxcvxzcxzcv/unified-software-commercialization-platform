package generation

import (
	"bytes"
	"fmt"
)

func createIntegrationFile(rendered []byte, merge *MergeSpec) ([]byte, string, error) {
	if err := validateMergeSpec(merge); err != nil {
		return nil, "", err
	}
	body := normalizedRegionBody(rendered)
	begin, end := regionMarkers(merge)
	content := append([]byte(begin+"\n"), body...)
	content = append(content, []byte(end+"\n")...)
	return content, digestBytes(body), nil
}

func mergeIntegrationRegion(current, rendered []byte, merge *MergeSpec) ([]byte, string, string, error) {
	if err := validateMergeSpec(merge); err != nil {
		return nil, "", "", err
	}
	begin, end := regionMarkers(merge)
	beginToken, endToken := []byte(begin), []byte(end)
	if bytes.Count(current, beginToken) != 1 || bytes.Count(current, endToken) != 1 {
		return nil, "", "", ErrIntegrationRegion
	}
	beginAt := bytes.Index(current, beginToken)
	endAt := bytes.Index(current, endToken)
	if beginAt < 0 || endAt <= beginAt || !lineBoundaryBefore(current, beginAt) || !lineBoundaryAfter(current, beginAt+len(beginToken)) || !lineBoundaryBefore(current, endAt) || !lineBoundaryAfter(current, endAt+len(endToken)) {
		return nil, "", "", ErrIntegrationRegion
	}
	bodyStart, ok := afterLineEnding(current, beginAt+len(beginToken))
	if !ok || bodyStart > endAt {
		return nil, "", "", ErrIntegrationRegion
	}
	currentBody := append([]byte(nil), current[bodyStart:endAt]...)
	newBody := normalizedRegionBody(rendered)
	merged := make([]byte, 0, len(current)-len(currentBody)+len(newBody))
	merged = append(merged, current[:bodyStart]...)
	merged = append(merged, newBody...)
	merged = append(merged, current[endAt:]...)
	return merged, digestBytes(currentBody), digestBytes(newBody), nil
}

func validateMergeSpec(merge *MergeSpec) error {
	if merge == nil || merge.Strategy != "generated_region_v1" || !stableIdentifierPattern.MatchString(merge.RegionID) || (merge.CommentPrefix != "//" && merge.CommentPrefix != "#") {
		return ErrInvalidInput
	}
	return nil
}

func regionMarkers(merge *MergeSpec) (string, string) {
	return fmt.Sprintf("%s <platform-generated:%s>", merge.CommentPrefix, merge.RegionID),
		fmt.Sprintf("%s </platform-generated:%s>", merge.CommentPrefix, merge.RegionID)
}

func normalizedRegionBody(value []byte) []byte {
	value = normalizeText(value)
	if len(value) == 0 || value[len(value)-1] != '\n' {
		value = append(value, '\n')
	}
	return value
}

func lineBoundaryBefore(value []byte, index int) bool {
	return index == 0 || value[index-1] == '\n'
}

func lineBoundaryAfter(value []byte, index int) bool {
	return index == len(value) || value[index] == '\n' || value[index] == '\r'
}

func afterLineEnding(value []byte, index int) (int, bool) {
	if index == len(value) {
		return index, true
	}
	if value[index] == '\n' {
		return index + 1, true
	}
	if value[index] == '\r' && index+1 < len(value) && value[index+1] == '\n' {
		return index + 2, true
	}
	return 0, false
}
