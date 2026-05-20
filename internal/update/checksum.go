package update

import (
	"fmt"
	"strings"
)

var (
	errAssetNotInChecksumFile = fmt.Errorf("asset not found in checksum file")
	errMalformedChecksumLine  = fmt.Errorf("malformed checksum line")
)

// parseChecksumFile parses GNU coreutils sha256sum format and returns the
// hex digest for the given assetName (R2.3).
func parseChecksumFile(content []byte, assetName string) (string, error) {
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		// Split on whitespace; require at least 2 fields.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", errMalformedChecksumLine
		}
		hex := fields[0]
		if len(hex) != 64 {
			return "", errMalformedChecksumLine
		}
		for _, r := range hex {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return "", errMalformedChecksumLine
			}
		}
		// The filename field may start with '*' (binary mode) or './'.
		name := fields[1]
		name = strings.TrimPrefix(name, "*")
		name = strings.TrimPrefix(name, "./")
		if name == assetName {
			return strings.ToLower(hex), nil
		}
	}
	return "", errAssetNotInChecksumFile
}
