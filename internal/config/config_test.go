package config

import "testing"

func TestValidateLoopbackOnly(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:7788": true,
		"localhost:7788": true,
		"[::1]:7788":     true,
		"0.0.0.0:7788":   false,
		":7788":          false,
		"10.0.0.1:7788":  false,
	}
	for addr, ok := range cases {
		err := Config{Addr: addr, HerdrCmd: []string{"herdr"}}.Validate()
		if (err == nil) != ok {
			t.Errorf("Validate(%q) err=%v, want ok=%v", addr, err, ok)
		}
	}
}
