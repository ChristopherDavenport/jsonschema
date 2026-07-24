package xvalid

import "testing"

func TestFormats(t *testing.T) {
	cases := []struct {
		format string
		value  string
		valid  bool
	}{
		{"date-time", "2020-01-01T00:00:00Z", true},
		{"date-time", "2020-13-01T00:00:00Z", false},
		{"date", "2020-02-29", true},
		{"date", "2020-02-30", false},
		{"duration", "P1Y2M3DT4H", true},
		{"duration", "P", false},
		{"email", "a@b.com", true},
		{"email", "Name <a@b.com>", false},
		{"ipv4", "1.2.3.4", true},
		{"ipv4", "::1", false},
		{"ipv6", "::1", true},
		{"uuid", "00000000-0000-0000-0000-000000000000", true},
		{"uuid", "nope", false},
		{"json-pointer", "/a/b", true},
		{"json-pointer", "a/b", false},
		{"regex", "^a.*$", true},
		{"regex", "(", false},
		{"unknown-format", "anything", true}, // unknown formats never fail
	}
	for _, tc := range cases {
		t.Run(tc.format+"/"+tc.value, func(t *testing.T) {
			got, _ := CheckFormat(tc.format, tc.value)
			if got != tc.valid {
				t.Fatalf("CheckFormat(%q, %q) = %v, want %v", tc.format, tc.value, got, tc.valid)
			}
		})
	}
}

func TestJSONEqual(t *testing.T) {
	if !JSONEqual(float64(1), float64(1.0)) {
		t.Error("1 should equal 1.0")
	}
	if JSONEqual("1", float64(1)) {
		t.Error(`"1" should not equal 1`)
	}
	if !JSONEqual([]any{float64(1), "a"}, []any{float64(1), "a"}) {
		t.Error("equal arrays should be equal")
	}
	if !JSONEqual(map[string]any{"a": float64(1)}, map[string]any{"a": float64(1)}) {
		t.Error("equal objects should be equal")
	}
	if JSONEqual([]any{float64(1)}, []any{float64(2)}) {
		t.Error("different arrays should differ")
	}
}
