package unisub

import (
	"ai-unisub/internal/aiprovider"
	"testing"
)

func TestPersistedCallHTTPCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   *aiprovider.AIProviderCallTrace
		want int
	}{
		{name: "nil", want: 0},
		{name: "success uses response status", in: &aiprovider.AIProviderCallTrace{ResponseStatus: 200}, want: 200},
		{name: "upstream error uses response status", in: &aiprovider.AIProviderCallTrace{ResponseStatus: 429, HTTPErrorCode: 429}, want: 429},
		{name: "local provider error code", in: &aiprovider.AIProviderCallTrace{HTTPErrorCode: 401, HTTPErrorInfo: "token"}, want: 401},
		{name: "network failure stays zero", in: &aiprovider.AIProviderCallTrace{HTTPErrorInfo: "dial tcp: i/o timeout"}, want: 0},
		{name: "response status wins over error code", in: &aiprovider.AIProviderCallTrace{ResponseStatus: 200, HTTPErrorCode: 502}, want: 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := persistedCallHTTPCode(tc.in); got != tc.want {
				t.Fatalf("persistedCallHTTPCode() = %d, want %d", got, tc.want)
			}
		})
	}
}
