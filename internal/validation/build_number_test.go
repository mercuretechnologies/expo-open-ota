package validation

import (
	"github.com/stretchr/testify/require"
	"testing"
	"xprem/internal/types"
)

func TestBuildNumber(t *testing.T) {
	for _, tc := range []struct {
		platform types.Platform
		value    string
		valid    bool
	}{
		{"android", "0", true}, {"android", "2100000000", true},
		{"android", "2100000001", false}, {"android", "9223372036854775808", false},
		{"android", "1.2.0", false}, {"android", "01", false},
		{"ios", "0", true}, {"ios", "2100000001", true},
		{"ios", "9223372036854775808", true}, {"ios", "1.2.0", true},
		{"ios", "1..2", false}, {"ios", "1.", false}, {"ios", "1.02", false},
		{"ios", "-1", false}, {"ios", "1e3", false}, {"ios", "+1", false},
		{"ios", " 1", false}, {"ios", "", false}, {"windows", "1", false},
	} {
		t.Run(string(tc.platform)+"/"+tc.value, func(t *testing.T) {
			err := BuildNumber(tc.platform, tc.value)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
