package common

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestParseGeminiUsageCount(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      int64
		ok        bool
	}{
		{"zero", `0`, 0, true},
		{"negative zero", `-0`, 0, true},
		{"integer", `123`, 123, true},
		{"quoted integer", `"123"`, 123, true},
		{"integral decimal", `1.0`, 1, true},
		{"quoted decimal", `"1.000"`, 1, true},
		{"exponent", `1e2`, 100, true},
		{"decimal exponent", `"1.01e2"`, 101, true},
		{"negative exponent integral", `100e-2`, 1, true},
		{"over float exactness", `9007199254740993`, 9007199254740993, true},
		{"quoted over float exactness", `"9007199254740993"`, 9007199254740993, true},
		{"exact exponent over float exactness", `9.007199254740993e15`, 9007199254740993, true},
		{"maximum", `9223372036854775807`, 9223372036854775807, true},
		{"maximum exponent", `"9.223372036854775807e18"`, 9223372036854775807, true},
		{"overflow", `9223372036854775808`, 0, false},
		{"overflow exponent", `1e19`, 0, false},
		{"huge exponent", `"1e99999999999999999999"`, 0, false},
		{"zero huge exponent", `"0e99999999999999999999"`, 0, true},
		{"fraction", `1.5`, 0, false},
		{"quoted fraction", `"1.5"`, 0, false},
		{"negative exponent fraction", `1e-2`, 0, false},
		{"negative", `-1`, 0, false},
		{"quoted negative", `"-1"`, 0, false},
		{"null", `null`, 0, false},
		{"array", `[]`, 0, false},
		{"boolean", `true`, 0, false},
		{"text", `"nan"`, 0, false},
		{"empty", `""`, 0, false},
		{"unfinished exponent", `"1e"`, 0, false},
		{"unfinished decimal", `"1."`, 0, false},
		{"overlong", `"` + strings.Repeat("0", 4097) + `"`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseGeminiUsageCount(gjson.Parse(tc.raw))
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ParseGeminiUsageCount(%s) = %d, %t; want %d, %t", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}
