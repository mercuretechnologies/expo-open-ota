package validation

import (
	"regexp"
	"strconv"
	"xprem/internal/types"
)

const MaxAndroidBuildNumber = 2_100_000_000

var integerBuildNumber = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var iosBuildNumber = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))*$`)

func BuildNumber(platform types.Platform, value string) error {
	switch platform {
	case types.PlatformAndroid:
		n, err := strconv.ParseInt(value, 10, 64)
		if !integerBuildNumber.MatchString(value) || err != nil || n > MaxAndroidBuildNumber {
			return Errorf("buildNumber", "Android build number must be an integer from 0 to %d", MaxAndroidBuildNumber)
		}
	case types.PlatformIOS:
		if !iosBuildNumber.MatchString(value) {
			return Errorf("buildNumber", "iOS build number must contain non-negative decimal integers separated by dots, without leading zeros")
		}
	default:
		return Errorf("platform", "unsupported build platform")
	}
	return nil
}
