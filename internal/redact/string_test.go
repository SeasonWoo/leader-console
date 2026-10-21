package redact

import (
	"strings"
	"testing"
)

func TestCredentials(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AKIAIOSFODNN7EXAMPLE", "AKIA****"},
		{"AKLTabcdefghijklmnop1234", "AKLT****"},
		{"sk-abcdefghijklmnop", "sk-a****"},
		{"ghp_abcdefghijklmnop", "ghp_****"},
		{"glpat-abcdefghijklmnop", "glpa****"},
		{"xoxb-123456789-abcdefgh", "xoxb****"},
		{"xoxp-123456789-abcdefgh", "xoxp****"},
		{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature", "eyJh****"},
		{"Authorization: Bearer abcdefghijk", "Authorization: Bearer abcd****"},
		{"bearer\tabcdefghijk", "bearer\tabcd****"},
		{"password=abcdefgh", "password=abcd****"},
		{"passwd : abcdefgh", "passwd : abcd****"},
		{"secret=abcdefgh", "secret=abcd****"},
		{"token=sk-abcdefghijklmnop", "token=sk-a****"},
		{"api_key=abcdefgh", "api_key=abcd****"},
		{"api-key: abcdefgh", "api-key: abcd****"},
		{"access_key=abcdefgh", "access_key=abcd****"},
		{"access-key: abcdefgh", "access-key: abcd****"},
		{"auth_key=abcdefgh", "auth_key=abcd****"},
		{"auth-key: abcdefgh", "auth-key: abcd****"},
		{"secret_key=abcdefgh", "secret_key=abcd****"},
		{"SECRET-KEY: abcdefgh", "SECRET-KEY: abcd****"},
		{`{"token":"a long secret value", "password": "abcdefgh"}`, `{"token":"a lo****", "password": "abcd****"}`},
		{"password='a long secret value'", "password='a lo****'"},
		{"password=abc", "password=****"},
		{"password=中文密码很长", "password=中文密码****"},
		{`token=""`, `token=""`},
		{"before\n-----BEGIN PRIVATE KEY-----\nabc\ndef\n-----END PRIVATE KEY-----\nafter", "before\n[REDACTED PRIVATE KEY]\nafter"},
		{"-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----", "[REDACTED PRIVATE KEY]"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----", "[REDACTED PRIVATE KEY]"},
		{"-----BEGIN EC PRIVATE KEY-----\nunterminated", "[REDACTED PRIVATE KEY]"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := String(tc.in); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
			if got := String(tc.want); got != tc.want {
				t.Fatalf("not idempotent: %q", got)
			}
		})
	}
}

func TestOrdinaryText(t *testing.T) {
	for _, s := range []string{
		"bfd1f64ab01234567890abcdef1234567890abcdef",
		"e4916e66-03ee-408d-88c3-ff6c201bf695",
		"token 数量为 100，password 校验通过",
		"the secret of the api is documented",
		"https://example.com/docs?task=hello",
		"mytoken=ordinary identifier, notpassword=ordinary identifier",
		"-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----",
		"", "tokenization: ordinary", "sk-short",
	} {
		if got := String(s); got != s {
			t.Errorf("false positive: %q => %q", s, got)
		}
	}
	combined := "password=abcdefgh, token=123456789; ghp_abcdefghijklmnop"
	if got := String(combined); strings.Contains(got, "efgh") || strings.Contains(got, "56789") || strings.Contains(got, "mnop") {
		t.Fatal(got)
	}
}
